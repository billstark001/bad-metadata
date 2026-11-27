package cleaner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Scan returns a preview only. Links, reparse points, and quarantine stores are
// never traversed. Metadata directory candidates are treated as single items.
func Scan(ctx context.Context, root string, opts Options) (Plan, error) {
	p := Plan{CreatedAt: time.Now().UTC(), Items: []Item{}, Issues: []Issue{}}
	kinds := opts.Kinds
	if kinds == nil {
		kinds = DefaultKinds()
	}
	selected := make(map[Kind]bool)
	for _, k := range kinds {
		switch k {
		case AppleDouble, DSStore, Spotlight, FSEvents, Trashes:
			selected[k] = true
		default:
			return p, fmt.Errorf("unknown metadata type %q", k)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil || strings.TrimSpace(root) == "" {
		return p, errors.New("a directory path is required")
	}
	p.Root = filepath.Clean(abs)
	if err = checkAbsolute(p.Root); err != nil {
		return p, err
	}
	r, err := os.OpenRoot(p.Root)
	if err != nil {
		return p, err
	}
	defer r.Close()
	p.rootInfo, err = r.Stat(".")
	if err != nil {
		return p, err
	}
	if err = rejectStoreRoot(p.Root); err != nil {
		return p, err
	}
	err = fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			p.Issues = append(p.Issues, Issue{path, walkErr.Error()})
			return nil
		}
		if path == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			p.Issues = append(p.Issues, Issue{path, infoErr.Error()})
			return nil
		}
		if isLink(info) {
			p.Issues = append(p.Issues, Issue{path, "skipped symbolic link or reparse point"})
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), quarantineDir) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		kind, err := match(r, path, info)
		if err != nil {
			p.Issues = append(p.Issues, Issue{path, err.Error()})
			return nil
		}
		if !selected[kind] {
			if kind != "" && info.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fingerprint, size, err := snapshot(ctx, r, path)
		if err != nil {
			p.Issues = append(p.Issues, Issue{path, err.Error()})
		} else {
			p.Items = append(p.Items, Item{Path: path, Kind: kind, IsDir: info.IsDir(), Bytes: size, Fingerprint: fingerprint})
			p.Bytes += size
		}
		if info.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	p.approved = append([]Item{}, p.Items...)
	p.ready = err == nil
	return p, err
}

func match(r *os.Root, path string, info fs.FileInfo) (Kind, error) {
	name := filepath.Base(path)
	if info.IsDir() {
		switch name {
		case ".Spotlight-V100":
			return Spotlight, nil
		case ".fseventsd":
			return FSEvents, nil
		case ".Trashes", ".Trash":
			return Trashes, nil
		}
		return "", nil
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	if name == ".DS_Store" {
		return DSStore, nil
	}
	if strings.HasPrefix(name, "._") && len(name) > 2 {
		f, err := r.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		var header [26]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return "", nil
			}
			return "", err
		}
		if binary.BigEndian.Uint32(header[0:4]) == 0x00051607 &&
			(binary.BigEndian.Uint32(header[4:8]) == 0x00020000 || binary.BigEndian.Uint32(header[4:8]) == 0x00010000) {
			return AppleDouble, nil
		}
	}
	return "", nil
}

// snapshot hashes contents as well as layout. A stale preview cannot silently
// clean newly added user data inside a metadata directory.
func snapshot(ctx context.Context, r *os.Root, path string) (string, int64, error) {
	h := sha256.New()
	var size int64
	err := fs.WalkDir(r.FS(), path, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if isLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("unsafe entry %q: links and special files are not cleaned", name)
		}
		relName := strings.TrimPrefix(name, path)
		entrySize := info.Size()
		if info.IsDir() {
			entrySize = 0
		}
		// Content and layout determine staleness. Directory timestamps can be
		// updated lazily by Windows even after their children have been closed.
		fmt.Fprintf(h, "%q\x00%d\x00%d\x00", relName, info.Mode(), entrySize)
		if !info.IsDir() {
			f, err := r.Open(name)
			if err != nil {
				return err
			}
			buf := make([]byte, 128*1024)
			for {
				if err = ctx.Err(); err != nil {
					break
				}
				n, readErr := f.Read(buf)
				if n > 0 {
					_, _ = h.Write(buf[:n])
					size += int64(n)
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						err = readErr
					}
					break
				}
			}
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), size, err
}

func checkAbsolute(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if isLink(info) {
			return fmt.Errorf("symbolic links and reparse points are not allowed in root paths: %s", current)
		}
		parent := filepath.Dir(current)
		if current == parent {
			return nil
		}
	}
}

func rejectStoreRoot(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		if strings.EqualFold(filepath.Base(current), quarantineDir) {
			return errors.New("cannot scan or clean inside a quarantine store")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func checkRelative(r *os.Root, path string) error {
	if !filepath.IsLocal(filepath.FromSlash(path)) || path == "." || strings.Contains(path, "\\") {
		return fmt.Errorf("unsafe relative path %q", path)
	}
	for current := path; current != "."; current = filepath.ToSlash(filepath.Dir(current)) {
		info, err := r.Lstat(current)
		if err != nil {
			return err
		}
		if isLink(info) {
			return fmt.Errorf("symbolic link or reparse point at %q", current)
		}
	}
	return nil
}
