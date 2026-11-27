package cleaner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type record struct {
	Item
	State string `json:"state"`
}

type manifest struct {
	Schema    int       `json:"schema"`
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"created_at"`
	Records   []record  `json:"records"`
}

// Execute applies an in-process preview. An exported JSON preview is not an
// executable plan. Each candidate is checked again before acting on it.
func Execute(ctx context.Context, plan Plan, mode Mode) (Result, error) {
	result := Result{Completed: []string{}, Issues: []Issue{}}
	if mode != Quarantine && mode != Delete {
		return result, fmt.Errorf("invalid cleanup mode %q", mode)
	}
	if !plan.ready || plan.rootInfo == nil || !slices.Equal(plan.Items, plan.approved) {
		return result, errors.New("a fresh, unmodified scan is required")
	}
	if err := ctx.Err(); err != nil {
		result.Cancelled = true
		return result, err
	}
	if err := checkAbsolute(plan.Root); err != nil {
		return result, err
	}
	r, err := os.OpenRoot(plan.Root)
	if err != nil {
		return result, err
	}
	defer r.Close()
	info, err := r.Stat(".")
	if err != nil || !os.SameFile(info, plan.rootInfo) {
		return result, errors.New("scan root changed; scan again")
	}
	if len(plan.Items) == 0 {
		return result, nil
	}
	var session string
	m := manifest{Schema: 1, Root: plan.Root, CreatedAt: time.Now().UTC(), Records: []record{}}
	if mode == Quarantine {
		session, err = newSession(r)
		if err != nil {
			return result, err
		}
		result.Session = filepath.Join(plan.Root, filepath.FromSlash(session))
		// Persist every intended move before moving anything. Pending records
		// are recoverable even if the process stops before the final journal.
		for _, item := range plan.Items {
			m.Records = append(m.Records, record{Item: item, State: "pending"})
		}
		if err := saveManifest(r, session, m); err != nil {
			return result, err
		}
	}
	for index, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			result.Cancelled = true
			return result, err
		}
		if err := validateCandidate(ctx, r, item); err != nil {
			if ctx.Err() != nil {
				result.Cancelled = true
				return result, ctx.Err()
			}
			result.Issues = append(result.Issues, Issue{item.Path, err.Error()})
			continue
		}
		if mode == Quarantine {
			target := session + "/data/" + item.Path
			if err = r.MkdirAll(filepath.ToSlash(filepath.Dir(target)), 0700); err != nil {
				return result, err
			}
			if err = checkRelative(r, filepath.ToSlash(filepath.Dir(target))); err != nil {
				return result, err
			}
			if err = r.Rename(item.Path, target); err != nil {
				result.Issues = append(result.Issues, Issue{item.Path, err.Error()})
				continue
			}
			result.Completed = append(result.Completed, item.Path)
			m.Records[index].State = "moved"
		} else {
			if err = r.RemoveAll(item.Path); err != nil {
				result.Issues = append(result.Issues, Issue{item.Path, err.Error()})
			} else {
				result.Completed = append(result.Completed, item.Path)
			}
		}
	}
	if mode == Quarantine {
		if err = saveManifest(r, session, m); err != nil {
			return result, fmt.Errorf("files were quarantined; keep session for recovery: %w", err)
		}
	}
	return result, nil
}

func validateCandidate(ctx context.Context, r *os.Root, item Item) error {
	if err := checkRelative(r, item.Path); err != nil {
		return err
	}
	info, err := r.Lstat(item.Path)
	if err != nil {
		return err
	}
	kind, err := match(r, item.Path, info)
	if err != nil {
		return err
	}
	if kind == "" || kind != item.Kind || info.IsDir() != item.IsDir {
		return errors.New("metadata type changed; scan again")
	}
	fingerprint, _, err := snapshot(ctx, r, item.Path)
	if err != nil {
		return err
	}
	if fingerprint != item.Fingerprint {
		return errors.New("contents changed since preview; scan again")
	}
	return nil
}

func newSession(r *os.Root) (string, error) {
	if err := r.Mkdir(quarantineDir, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if err := checkRelative(r, quarantineDir); err != nil {
		return "", err
	}
	info, err := r.Lstat(quarantineDir)
	if err != nil || !info.IsDir() {
		return "", errors.New("quarantine store is not a directory")
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	session := quarantineDir + "/" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:])
	if err := r.Mkdir(session, 0700); err != nil {
		return "", err
	}
	if err := r.Mkdir(session+"/data", 0700); err != nil {
		return "", err
	}
	return session, nil
}

func saveManifest(r *os.Root, session string, m manifest) error {
	if err := checkRelative(r, session); err != nil {
		return err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	temp := session + "/manifest-" + hex.EncodeToString(suffix[:]) + ".tmp"
	f, err := r.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = WriteJSON(f, m)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = r.Remove(temp)
		return err
	}
	if err = r.Rename(temp, session+"/manifest.json"); err != nil {
		_ = r.Remove(temp)
	}
	return err
}

// Restore moves a session's saved entries back without replacing any existing
// destination. The store must stay under the original root. It is never purged.
func Restore(ctx context.Context, sessionPath string) (Result, error) {
	result := Result{Completed: []string{}, Issues: []Issue{}}
	abs, err := filepath.Abs(sessionPath)
	if err != nil || sessionPath == "" {
		return result, errors.New("a quarantine session directory is required")
	}
	abs = filepath.Clean(abs)
	if filepath.Base(filepath.Dir(abs)) != quarantineDir {
		return result, errors.New("session must be directly inside .badmeta-quarantine")
	}
	if err = checkAbsolute(abs); err != nil {
		return result, err
	}
	root := filepath.Dir(filepath.Dir(abs))
	r, err := os.OpenRoot(root)
	if err != nil {
		return result, err
	}
	defer r.Close()
	session := quarantineDir + "/" + filepath.Base(abs)
	result.Session = abs
	if err := checkRelative(r, session+"/manifest.json"); err != nil {
		return result, err
	}
	f, err := r.Open(session + "/manifest.json")
	if err != nil {
		return result, err
	}
	var m manifest
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&m)
	if err == nil {
		var extra any
		if tailErr := decoder.Decode(&extra); !errors.Is(tailErr, io.EOF) {
			err = errors.New("manifest must contain exactly one JSON object")
		}
	}
	_ = f.Close()
	if err != nil {
		return result, fmt.Errorf("invalid quarantine manifest: %w", err)
	}
	if m.Schema != 1 || !sameRootPath(r, m.Root, root) {
		return result, errors.New("manifest schema or original root does not match session")
	}
	if err = validateRecords(m.Records); err != nil {
		return result, err
	}
	for i := range m.Records {
		entry := &m.Records[i]
		if err := ctx.Err(); err != nil {
			result.Cancelled = true
			return result, err
		}
		if entry.State == "restored" {
			continue
		}
		source := session + "/data/" + entry.Path
		if _, err = r.Lstat(source); errors.Is(err, fs.ErrNotExist) {
			// A crash after the restore rename can leave the saved journal at
			// "moved". Recognize the exact restored contents without moving again.
			if checkRelative(r, entry.Path) == nil {
				fingerprint, _, snapErr := snapshot(ctx, r, entry.Path)
				if snapErr == nil && fingerprint == entry.Fingerprint {
					entry.State = "restored"
					continue
				}
			}
			if entry.State == "pending" {
				continue // The journal was saved before an unsuccessful move.
			}
		}
		if err = checkRelative(r, source); err != nil {
			result.Issues = append(result.Issues, Issue{entry.Path, err.Error()})
			continue
		}
		fingerprint, _, snapErr := snapshot(ctx, r, source)
		if ctx.Err() != nil {
			result.Cancelled = true
			return result, ctx.Err()
		}
		if snapErr != nil || fingerprint != entry.Fingerprint {
			result.Issues = append(result.Issues, Issue{entry.Path, "quarantined contents changed or could not be read"})
			continue
		}
		sourceInfo, statErr := r.Lstat(source)
		if statErr != nil {
			result.Issues = append(result.Issues, Issue{entry.Path, statErr.Error()})
			continue
		}
		kind, matchErr := match(r, source, sourceInfo)
		if matchErr != nil || kind != entry.Kind || sourceInfo.IsDir() != entry.IsDir {
			result.Issues = append(result.Issues, Issue{entry.Path, "manifest metadata type does not match quarantined entry"})
			continue
		}
		parent := filepath.ToSlash(filepath.Dir(entry.Path))
		if err = ensureParents(r, parent); err != nil {
			result.Issues = append(result.Issues, Issue{entry.Path, err.Error()})
			continue
		}
		if _, err = r.Lstat(entry.Path); !errors.Is(err, fs.ErrNotExist) {
			result.Issues = append(result.Issues, Issue{entry.Path, "destination already exists or cannot be checked; nothing overwritten"})
			continue
		}
		if err = checkAbsolute(root); err != nil {
			return result, err
		}
		openedRootInfo, statErr := r.Stat(".")
		currentRootInfo, pathErr := os.Stat(root)
		if statErr != nil || pathErr != nil || !os.SameFile(openedRootInfo, currentRootInfo) {
			return result, errors.New("restore root changed during operation")
		}
		// The native no-replace operation makes the conflict check atomic too.
		if err = renameNoReplace(r, source, entry.Path); err != nil {
			result.Issues = append(result.Issues, Issue{entry.Path, err.Error()})
			continue
		}
		result.Completed = append(result.Completed, entry.Path)
		entry.State = "restored"
	}
	if err = saveManifest(r, session, m); err != nil {
		return result, fmt.Errorf("restored files but could not update journal: %w", err)
	}
	return result, nil
}

func sameRootPath(r *os.Root, saved, current string) bool {
	saved = filepath.Clean(saved)
	if saved == current {
		return true
	}
	// Dialogs may normalize drive-letter or directory casing. Verify physical
	// identity as well, so case-sensitive volumes still distinguish both names.
	if !strings.EqualFold(saved, current) {
		return false
	}
	savedInfo, err := os.Stat(saved)
	if err != nil {
		return false
	}
	currentInfo, err := r.Stat(".")
	return err == nil && os.SameFile(savedInfo, currentInfo)
}

func ensureParents(r *os.Root, path string) error {
	if path == "." {
		return nil
	}
	if err := ensureParents(r, filepath.ToSlash(filepath.Dir(path))); err != nil {
		return err
	}
	if err := r.Mkdir(path, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := checkRelative(r, path); err != nil {
		return err
	}
	info, err := r.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("restore parent %q is not a directory", path)
	}
	return nil
}

func validateRecords(records []record) error {
	seen := map[string]bool{}
	for _, record := range records {
		path := record.Path
		if !fs.ValidPath(path) || !filepath.IsLocal(filepath.FromSlash(path)) || path == "." || strings.Contains(path, "\\") ||
			strings.EqualFold(strings.Split(path, "/")[0], quarantineDir) || len(record.Fingerprint) != 64 {
			return fmt.Errorf("unsafe manifest entry %q", path)
		}
		if record.State != "pending" && record.State != "moved" && record.State != "restored" {
			return fmt.Errorf("invalid journal state for %q", path)
		}
		key := path
		if seen[key] {
			return fmt.Errorf("duplicate manifest entry %q", path)
		}
		seen[key] = true
	}
	for path := range seen {
		for parent := filepath.ToSlash(filepath.Dir(path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return fmt.Errorf("overlapping manifest entries at %q", path)
			}
		}
	}
	return nil
}
