package cleaner

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCleanupPreservesMetadataInsideExcludedTrash(t *testing.T) {
	root := fixtureRoot(t)
	apple := make([]byte, 32)
	binary.BigEndian.PutUint32(apple[:4], 0x00051607)
	binary.BigEndian.PutUint32(apple[4:8], 0x00020000)
	protected := map[string][]byte{
		".Trashes/501/.DS_Store": []byte("trash Finder metadata"),
		".Trashes/501/._photo":   apple,
		".Trash/.DS_Store":       []byte("local trash Finder metadata"),
		".Trash/._photo":         apple,
	}
	for path, content := range protected {
		putFixture(t, root, path, content)
	}
	putFixture(t, root, ".DS_Store", []byte("outside trash"))
	plan := scanFixture(t, root, Options{})
	if len(plan.Items) != 1 || plan.Items[0].Path != ".DS_Store" {
		t.Fatalf("default scan entered excluded trash: %#v", plan.Items)
	}
	result, err := Execute(context.Background(), plan, Quarantine)
	if err != nil || len(result.Issues) != 0 || len(result.Completed) != 1 {
		t.Fatalf("default quarantine = %#v, %v", result, err)
	}
	for path, content := range protected {
		assertFixture(t, root, path, content)
	}
}

func TestSelectedFilesDoNotEnterExcludedMetadataDirectories(t *testing.T) {
	root := fixtureRoot(t)
	protected := map[string][]byte{
		".Spotlight-V100/.DS_Store": []byte("inside Spotlight"),
		".fseventsd/.DS_Store":      []byte("inside events"),
		".Trashes/501/.DS_Store":    []byte("inside trash"),
	}
	for path, content := range protected {
		putFixture(t, root, path, content)
	}
	putFixture(t, root, ".DS_Store", []byte("root metadata"))
	putFixture(t, root, "ordinary/sub/.DS_Store", []byte("nested metadata"))
	plan := scanFixture(t, root, Options{Kinds: []Kind{DSStore}})
	if len(plan.Items) != 2 {
		t.Fatalf("DSStore-only scan entered excluded categories: %#v", plan.Items)
	}
	for _, item := range plan.Items {
		if item.Path != ".DS_Store" && item.Path != "ordinary/sub/.DS_Store" {
			t.Fatalf("selected metadata inside an excluded category: %#v", item)
		}
	}
	result, err := Execute(context.Background(), plan, Quarantine)
	if err != nil || len(result.Issues) != 0 || len(result.Completed) != 2 {
		t.Fatalf("DSStore-only quarantine = %#v, %v", result, err)
	}
	for path, content := range protected {
		assertFixture(t, root, path, content)
	}
}

func TestRestoreRejectsTrailingManifestContentBeforeMovingFiles(t *testing.T) {
	for _, tail := range []string{"{}", "null", "invalid JSON"} {
		t.Run(tail, func(t *testing.T) {
			root, session := quarantinedFixture(t)
			manifestPath := filepath.Join(session, "manifest.json")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, append(data, []byte("\n"+tail)...), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := Restore(context.Background(), session)
			if err == nil || len(result.Completed) != 0 {
				t.Fatalf("trailing manifest content accepted: %#v, %v", result, err)
			}
			assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
			if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid manifest changed the destination: %v", err)
			}
		})
	}
}

func TestManifestPathValidationUsesExactCanonicalNames(t *testing.T) {
	entry := func(path string, kind Kind, directory bool) record {
		return record{Item: Item{Path: path, Kind: kind, IsDir: directory, Fingerprint: strings.Repeat("a", 64)}, State: "moved"}
	}
	tests := []struct {
		name    string
		records []record
		valid   bool
	}{
		{
			name: "case-distinct paths remain valid",
			records: []record{
				entry("A/.DS_Store", DSStore, false),
				entry("a/.DS_Store", DSStore, false),
			},
			valid: true,
		},
		{
			name: "exact duplicate",
			records: []record{
				entry("a/.DS_Store", DSStore, false),
				entry("a/.DS_Store", DSStore, false),
			},
		},
		{
			name: "overlapping directory and child",
			records: []record{
				entry(".Spotlight-V100", Spotlight, true),
				entry(".Spotlight-V100/.DS_Store", DSStore, false),
			},
		},
		{
			name:    "noncanonical parent traversal",
			records: []record{entry("a/../.DS_Store", DSStore, false)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRecords(test.records)
			if (err == nil) != test.valid {
				t.Fatalf("manifest validity = %v, want %v: %v", err == nil, test.valid, err)
			}
		})
	}
}

func TestInterruptedRestoreRecognizesAlreadyMovedFileAndUpdatesJournal(t *testing.T) {
	root, session := quarantinedFixture(t)
	before := readFixtureManifest(t, session)
	if len(before.Records) != 1 || before.Records[0].State != "moved" {
		t.Fatalf("unexpected quarantine journal: %#v", before.Records)
	}
	// Reproduce a crash between the restore rename and its journal update.
	if err := os.Rename(filepath.Join(session, "data", ".DS_Store"), filepath.Join(root, ".DS_Store")); err != nil {
		t.Fatal(err)
	}
	result, err := Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 0 {
		t.Fatalf("interrupted restore recovery = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("original metadata"))
	after := readFixtureManifest(t, session)
	if len(after.Records) != 1 || after.Records[0].State != "restored" {
		t.Fatalf("recovery did not persist the restored journal state: %#v", after.Records)
	}
	result, err = Restore(context.Background(), session)
	if err != nil || len(result.Completed) != 0 || len(result.Issues) != 0 {
		t.Fatalf("repeated recovered restore = %#v, %v", result, err)
	}
	assertFixture(t, root, ".DS_Store", []byte("original metadata"))
}
