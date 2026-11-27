package cleaner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestMetadataCategoriesAndTrashOptIn(t *testing.T) {
	root := fixtureRoot(t)
	apple := make([]byte, 32)
	binary.BigEndian.PutUint32(apple[:4], 0x00051607)
	binary.BigEndian.PutUint32(apple[4:8], 0x00020000)
	for path, content := range map[string][]byte{
		"._picture.jpg":                                     apple,
		".DS_Store":                                         []byte("finder"),
		"sub/.DS_Store":                                     []byte("nested"),
		".Spotlight-V100/index":                             []byte("index"),
		".Spotlight-V100/.DS_Store":                         []byte("inside index"),
		".fseventsd/events":                                 []byte("events"),
		".Trashes/501/recoverable.txt":                      []byte("user trash"),
		".Trash/recoverable.txt":                            []byte("user trash too"),
		".badmeta-quarantine/old/data/sub/.DS_Store":        []byte("saved metadata"),
		"ordinary.txt":                                      []byte("keep me"),
		"._not-appledouble":                                 []byte("ordinary dot underscore file"),
		".Spotlight-V101/ordinary.txt":                      []byte("different name"),
		"metadata-lookalike/.fseventsd-backup/ordinary.txt": []byte("keep me too"),
	} {
		putFixture(t, root, path, content)
	}
	plan := scanFixture(t, root, Options{})
	if len(plan.Items) != 5 {
		t.Fatalf("default candidates = %#v", plan.Items)
	}
	for _, item := range plan.Items {
		if item.Kind == Trashes || strings.HasPrefix(item.Path, quarantineDir+"/") {
			t.Fatalf("default plan included protected item: %#v", item)
		}
	}
	empty := scanFixture(t, root, Options{Kinds: []Kind{}})
	if len(empty.Items) != 0 {
		t.Fatalf("explicit empty category list matched items: %#v", empty.Items)
	}
	kinds := append(DefaultKinds(), Trashes)
	plan = scanFixture(t, root, Options{Kinds: kinds})
	if len(plan.Items) != 7 {
		t.Fatalf("trash-inclusive candidates = %#v", plan.Items)
	}
	result, err := Execute(context.Background(), plan, Quarantine)
	if err != nil || len(result.Issues) != 0 || len(result.Completed) != 7 {
		t.Fatalf("quarantine = %#v, %v", result, err)
	}
	assertFixture(t, root, "ordinary.txt", []byte("keep me"))
	assertFixture(t, root, "._not-appledouble", []byte("ordinary dot underscore file"))
	assertFixture(t, root, ".badmeta-quarantine/old/data/sub/.DS_Store", []byte("saved metadata"))
	restored, err := Restore(context.Background(), result.Session)
	if err != nil || len(restored.Issues) != 0 || len(restored.Completed) != 7 {
		t.Fatalf("restore = %#v, %v", restored, err)
	}
	assertFixture(t, root, "._picture.jpg", apple)
	assertFixture(t, root, ".Spotlight-V100/.DS_Store", []byte("inside index"))
	assertFixture(t, root, ".Trashes/501/recoverable.txt", []byte("user trash"))
	assertFixture(t, root, ".Trash/recoverable.txt", []byte("user trash too"))
}

func TestInvalidAppleDoublePreserved(t *testing.T) {
	root := fixtureRoot(t)
	badMagic := make([]byte, 26)
	binary.BigEndian.PutUint32(badMagic[4:8], 0x00020000)
	badVersion := make([]byte, 26)
	binary.BigEndian.PutUint32(badVersion[:4], 0x00051607)
	binary.BigEndian.PutUint32(badVersion[4:8], 0xdeadbeef)
	for path, content := range map[string][]byte{
		"._short":   []byte{0, 5, 22, 7},
		"._magic":   badMagic,
		"._version": badVersion,
		"._":        []byte("ordinary"),
	} {
		putFixture(t, root, path, content)
	}
	putFixture(t, root, ".DS_Store", []byte("metadata"))
	plan := scanFixture(t, root, Options{})
	if len(plan.Items) != 1 || plan.Items[0].Kind != DSStore {
		t.Fatalf("invalid AppleDouble was selected: %#v", plan.Items)
	}
	result, err := Execute(context.Background(), plan, Delete)
	if err != nil || len(result.Completed) != 1 || len(result.Issues) != 0 {
		t.Fatalf("delete = %#v, %v", result, err)
	}
	assertFixture(t, root, "._magic", badMagic)
	assertFixture(t, root, "._version", badVersion)
	assertFixture(t, root, "._short", []byte{0, 5, 22, 7})
}

func TestMutatedOrImportedPlanRejected(t *testing.T) {
	for _, change := range []string{"path", "kind", "append", "remove", "fingerprint", "json"} {
		t.Run(change, func(t *testing.T) {
			root := fixtureRoot(t)
			putFixture(t, root, ".DS_Store", []byte("metadata"))
			putFixture(t, root, "important.txt", []byte("user data"))
			plan := scanFixture(t, root, Options{})
			switch change {
			case "path":
				plan.Items[0].Path = "important.txt"
			case "kind":
				plan.Items[0].Kind = Trashes
			case "append":
				plan.Items = append(plan.Items, Item{Path: "important.txt", Kind: DSStore})
			case "remove":
				plan.Items = nil
			case "fingerprint":
				plan.Items[0].Fingerprint = strings.Repeat("0", 64)
			case "json":
				data, err := json.Marshal(plan)
				if err != nil {
					t.Fatal(err)
				}
				plan = Plan{}
				if err := json.Unmarshal(data, &plan); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Execute(context.Background(), plan, Delete)
			if err == nil || len(result.Completed) != 0 {
				t.Fatalf("mutated plan executed: %#v, %v", result, err)
			}
			assertFixture(t, root, ".DS_Store", []byte("metadata"))
			assertFixture(t, root, "important.txt", []byte("user data"))
		})
	}
}

func TestChangedContentWithSameSizeAndTimestampRejected(t *testing.T) {
	root := fixtureRoot(t)
	path := filepath.Join(root, ".DS_Store")
	putFixture(t, root, ".DS_Store", []byte("original"))
	plan := scanFixture(t, root, Options{})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	putFixture(t, root, ".DS_Store", []byte("modified"))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	result, err := Execute(context.Background(), plan, Delete)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 1 {
		t.Fatalf("changed content result = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("modified"))
}

func TestAddedDirectoryContentRejected(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".Spotlight-V100/index", []byte("index"))
	plan := scanFixture(t, root, Options{})
	putFixture(t, root, ".Spotlight-V100/new-user-data", []byte("new data"))
	result, err := Execute(context.Background(), plan, Delete)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 1 {
		t.Fatalf("changed directory result = %#v, %v", result, err)
	}
	assertFixture(t, root, ".Spotlight-V100/new-user-data", []byte("new data"))
}

func TestReplacedRootRejected(t *testing.T) {
	base := fixtureRoot(t)
	root := filepath.Join(base, "root")
	putFixture(t, root, ".DS_Store", []byte("original metadata"))
	plan := scanFixture(t, root, Options{})
	if err := os.Rename(root, filepath.Join(base, "old-root")); err != nil {
		t.Fatal(err)
	}
	putFixture(t, root, ".DS_Store", []byte("replacement"))
	result, err := Execute(context.Background(), plan, Delete)
	if err == nil || len(result.Completed) != 0 {
		t.Fatalf("replaced root result = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("replacement"))
	assertFixture(t, filepath.Join(base, "old-root"), ".DS_Store", []byte("original metadata"))
}

func TestScanSkipsLinksAndRejectsLinkedRoots(t *testing.T) {
	base := fixtureRoot(t)
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	putFixture(t, outside, ".DS_Store", []byte("outside data"))
	putFixture(t, root, "keep.txt", []byte("inside data"))
	link := filepath.Join(root, ".Spotlight-V100")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	plan, err := Scan(context.Background(), root, Options{})
	if err != nil || len(plan.Items) != 0 || len(plan.Issues) == 0 {
		t.Fatalf("linked candidate = %#v, %v", plan, err)
	}
	if _, err := Scan(context.Background(), link, Options{}); err == nil {
		t.Fatal("scan accepted linked root")
	}
	assertFixture(t, outside, ".DS_Store", []byte("outside data"))
}

func TestWindowsJunctionSkipped(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction test")
	}
	base := fixtureRoot(t)
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	putFixture(t, outside, ".DS_Store", []byte("outside data"))
	putFixture(t, root, "keep.txt", []byte("inside data"))
	link := filepath.Join(root, ".Spotlight-V100")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	command := "New-Item -ItemType Junction -Path " + quote(link) + " -Target " + quote(outside) + " | Out-Null"
	if output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput(); err != nil {
		t.Skipf("junctions unavailable: %v: %s", err, output)
	}
	plan, err := Scan(context.Background(), root, Options{})
	if err != nil || len(plan.Items) != 0 || len(plan.Issues) == 0 {
		t.Fatalf("junction candidate = %#v, %v", plan, err)
	}
	if _, err := Scan(context.Background(), link, Options{}); err == nil {
		t.Fatal("scan accepted junction root")
	}
	assertFixture(t, outside, ".DS_Store", []byte("outside data"))
}

func TestLinkInsideMetadataDirectoryPreventsCleanup(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".Spotlight-V100/index", []byte("index"))
	putFixture(t, root, "important.txt", []byte("user data"))
	if err := os.Symlink(filepath.Join(root, "important.txt"), filepath.Join(root, ".Spotlight-V100", "linked")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	plan, err := Scan(context.Background(), root, Options{})
	if err != nil || len(plan.Items) != 0 || len(plan.Issues) == 0 {
		t.Fatalf("unsafe directory plan = %#v, %v", plan, err)
	}
	assertFixture(t, root, "important.txt", []byte("user data"))
}

func TestRestoreDoesNotOverwriteAndCanRetry(t *testing.T) {
	root, session := quarantinedFixture(t)
	putFixture(t, root, ".DS_Store", []byte("new destination data"))
	result, err := Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 1 {
		t.Fatalf("conflict result = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("new destination data"))
	assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
	if err := os.Remove(filepath.Join(root, ".DS_Store")); err != nil {
		t.Fatal(err)
	}
	result, err = Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 1 || len(result.Issues) != 0 {
		t.Fatalf("retry result = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("original metadata"))
	result, err = Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 0 {
		t.Fatalf("repeat restore result = %#v, %v", result, err)
	}
}

func TestNativeRenameNeverReplacesExistingDestination(t *testing.T) {
	root := fixtureRoot(t)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	putFixture(t, root, "source", []byte("saved data"))
	putFixture(t, root, "destination", []byte("existing user data"))
	if err := renameNoReplace(r, "source", "destination"); err == nil {
		t.Fatal("native rename replaced an existing destination")
	}
	assertFixture(t, root, "source", []byte("saved data"))
	assertFixture(t, root, "destination", []byte("existing user data"))
	if err := renameNoReplace(r, "source", "new-destination"); err != nil {
		t.Fatalf("native rename to absent destination failed: %v", err)
	}
	assertFixture(t, root, "new-destination", []byte("saved data"))
	if _, err := os.Stat(filepath.Join(root, "source")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source remains after rename: %v", err)
	}
}

func TestTamperedQuarantinedContentPreserved(t *testing.T) {
	root, session := quarantinedFixture(t)
	putFixture(t, session, "data/.DS_Store", []byte("tampered metadata"))
	result, err := Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 1 {
		t.Fatalf("tampered content result = %#v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered content restored: %v", err)
	}
	assertFixture(t, session, "data/.DS_Store", []byte("tampered metadata"))
}

func TestUnsafeManifestPathsRejected(t *testing.T) {
	for _, path := range []string{"../outside", "/absolute", "a/../../outside", ".", "", ".badmeta-quarantine/other", "a\\..\\outside"} {
		t.Run(path, func(t *testing.T) {
			root, session := quarantinedFixture(t)
			m := readFixtureManifest(t, session)
			m.Records[0].Path = path
			writeFixtureManifest(t, session, m)
			result, err := Restore(context.Background(), session)
			if err == nil || len(result.Completed) != 0 {
				t.Fatalf("unsafe path accepted: %#v, %v", result, err)
			}
			assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
			if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsafe manifest changed destination: %v", err)
			}
		})
	}
}

func TestInvalidManifestSchemaRootAndDuplicateRejected(t *testing.T) {
	for _, change := range []string{"schema", "root", "state", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			_, session := quarantinedFixture(t)
			m := readFixtureManifest(t, session)
			switch change {
			case "schema":
				m.Schema++
			case "root":
				m.Root = filepath.Dir(m.Root)
			case "state":
				m.Records[0].State = "unknown"
			case "duplicate":
				m.Records = append(m.Records, m.Records[0])
			}
			writeFixtureManifest(t, session, m)
			result, err := Restore(context.Background(), session)
			if err == nil || len(result.Completed) != 0 {
				t.Fatalf("invalid manifest accepted: %#v, %v", result, err)
			}
			assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
		})
	}
}

func TestPendingJournalCanRecoverCompletedMove(t *testing.T) {
	root, session := quarantinedFixture(t)
	m := readFixtureManifest(t, session)
	m.Records[0].State = "pending"
	writeFixtureManifest(t, session, m)
	result, err := Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 1 || len(result.Issues) != 0 {
		t.Fatalf("pending journal recovery = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("original metadata"))
}

func TestCancellationLeavesFilesUnchanged(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("metadata"))
	plan := scanFixture(t, root, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Execute(ctx, plan, Quarantine)
	if !errors.Is(err, context.Canceled) || !result.Cancelled || len(result.Completed) != 0 {
		t.Fatalf("cancelled execute = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("metadata"))
	if _, err := os.Stat(filepath.Join(root, quarantineDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled execute created a store: %v", err)
	}
	incomplete, err := Scan(ctx, root, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan error = %v", err)
	}
	if result, err := Execute(context.Background(), incomplete, Delete); err == nil || len(result.Completed) != 0 {
		t.Fatalf("incomplete scan executed: %#v, %v", result, err)
	}
	root, session := quarantinedFixture(t)
	result, err = Restore(ctx, session)
	if !errors.Is(err, context.Canceled) || !result.Cancelled || len(result.Completed) != 0 {
		t.Fatalf("cancelled restore = %#v, %v", result, err)
	}
	assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
	if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled restore changed destination: %v", err)
	}
}

func TestQuarantineStoresCannotBeScanned(t *testing.T) {
	_, session := quarantinedFixture(t)
	for _, root := range []string{filepath.Dir(session), session, filepath.Join(session, "data")} {
		if _, err := Scan(context.Background(), root, Options{}); err == nil {
			t.Fatalf("scan accepted quarantine root %s", root)
		}
	}
}

func TestInvalidModeAndCategoryRejected(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("metadata"))
	if _, err := Scan(context.Background(), root, Options{Kinds: []Kind{"unknown"}}); err == nil {
		t.Fatal("scan accepted unknown category")
	}
	if _, err := Execute(context.Background(), scanFixture(t, root, Options{}), Mode("unknown")); err == nil {
		t.Fatal("execute accepted unknown mode")
	}
	assertFixture(t, root, ".DS_Store", []byte("metadata"))
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func putFixture(t *testing.T, root, path string, data []byte) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFixture(t *testing.T, root, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}

func scanFixture(t *testing.T, root string, opts Options) Plan {
	t.Helper()
	plan, err := Scan(context.Background(), root, opts)
	if err != nil || len(plan.Issues) != 0 {
		t.Fatalf("scan = %#v, %v", plan, err)
	}
	return plan
}

func quarantinedFixture(t *testing.T) (string, string) {
	t.Helper()
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("original metadata"))
	result, err := Execute(context.Background(), scanFixture(t, root, Options{}), Quarantine)
	if err != nil || len(result.Completed) != 1 || len(result.Issues) != 0 || result.Session == "" {
		t.Fatalf("quarantine = %#v, %v", result, err)
	}
	return root, result.Session
}

func readFixtureManifest(t *testing.T, session string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(session, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeFixtureManifest(t *testing.T, session string, m manifest) {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	putFixture(t, session, "manifest.json", data)
}
