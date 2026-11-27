package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/billstark001/bad-metadata/internal/cleaner"
)

func TestParseInterspersedOptions(t *testing.T) {
	opts, err := parse([]string{"clean", "--types=dsstore,dsstore", "directory", "--json", "--trashes", "--mode", "quarantine", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "clean" || opts.path != "directory" || !opts.yes || !opts.json || opts.mode != cleaner.Quarantine {
		t.Fatalf("unexpected options: %#v", opts)
	}
	if !reflect.DeepEqual(opts.kinds, []cleaner.Kind{cleaner.DSStore, cleaner.Trashes}) {
		t.Fatalf("unexpected kinds: %v", opts.kinds)
	}
	literal, err := parse([]string{"--", "--literal-directory"})
	if err != nil || literal.path != "--literal-directory" {
		t.Fatalf("literal path: %#v, %v", literal, err)
	}
}

func TestInvalidUsage(t *testing.T) {
	for _, args := range [][]string{
		{"scan"},
		{"scan", "one", "two"},
		{"clean", "path", "--mode", "typo"},
		{"scan", "path", "--mode=delete"},
		{"scan", "path", "--yes"},
		{"scan", "path", "--types="},
		{"scan", "path", "--types=dsstore,unknown"},
		{"scan", "path", "--types"},
		{"scan", "path", "--unknown"},
		{"scan", "path", "--json=true"},
		{"restore", "path", "--trashes"},
		{"clean", "path", "--mode=delete", "--mode=quarantine"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := Run(context.Background(), args, &out, &diagnostics); code != 2 {
				t.Fatalf("code = %d; stdout = %s; stderr = %s", code, &out, &diagnostics)
			}
			if out.Len() != 0 || !strings.Contains(diagnostics.String(), "Error:") {
				t.Fatalf("invalid usage output: %s / %s", &out, &diagnostics)
			}
		})
	}
}

func TestPreviewAndUnconfirmedCleanNeverMutate(t *testing.T) {
	for _, command := range []string{"scan", "clean"} {
		t.Run(command, func(t *testing.T) {
			root := tempRoot(t)
			metadata := filepath.Join(root, ".DS_Store")
			writeFile(t, metadata, "Finder metadata")
			writeFile(t, filepath.Join(root, "ordinary.txt"), "keep me")
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), []string{command, root, "--json", "--types=dsstore"}, &out, &diagnostics)
			if code != 0 {
				t.Fatalf("code = %d; stderr = %s", code, &diagnostics)
			}
			var plan cleaner.Plan
			if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
				t.Fatalf("invalid JSON: %v; %s", err, &out)
			}
			if len(plan.Items) != 1 || plan.Items[0].Kind != cleaner.DSStore {
				t.Fatalf("unexpected preview: %#v", plan)
			}
			if got, err := os.ReadFile(metadata); err != nil || string(got) != "Finder metadata" {
				t.Fatalf("preview changed metadata: %q, %v", got, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 2 {
				t.Fatalf("preview created files: %v, %v", entries, err)
			}
		})
	}
}

func TestQuarantineAndRestoreThroughCLI(t *testing.T) {
	root := tempRoot(t)
	metadata := filepath.Join(root, ".DS_Store")
	ordinary := filepath.Join(root, "keep.txt")
	writeFile(t, metadata, "metadata payload")
	writeFile(t, ordinary, "ordinary payload")
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"clean", root, "--yes", "--json", "--types=dsstore"}, &out, &diagnostics)
	if code != 0 {
		t.Fatalf("clean code = %d; stdout = %s; stderr = %s", code, &out, &diagnostics)
	}
	var result cleaner.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Session == "" || len(result.Completed) != 1 {
		t.Fatalf("unexpected quarantine result: %#v", result)
	}
	if _, err := os.Stat(metadata); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata was not quarantined: %v", err)
	}
	if got, err := os.ReadFile(ordinary); err != nil || string(got) != "ordinary payload" {
		t.Fatalf("ordinary file changed: %q, %v", got, err)
	}
	if code := Run(context.Background(), []string{"restore", result.Session}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("unconfirmed restore code = %d", code)
	}
	if _, err := os.Stat(metadata); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unconfirmed restore changed metadata: %v", err)
	}
	out.Reset()
	diagnostics.Reset()
	code = Run(context.Background(), []string{"restore", result.Session, "--yes", "--json"}, &out, &diagnostics)
	if code != 0 {
		t.Fatalf("restore code = %d; stdout = %s; stderr = %s", code, &out, &diagnostics)
	}
	if got, err := os.ReadFile(metadata); err != nil || string(got) != "metadata payload" {
		t.Fatalf("restore failed: %q, %v", got, err)
	}
}

func TestExplicitDeleteThroughCLI(t *testing.T) {
	root := tempRoot(t)
	metadata := filepath.Join(root, ".DS_Store")
	writeFile(t, metadata, "metadata payload")
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"clean", "--mode=delete", root, "--yes"}, &out, &diagnostics)
	if code != 0 {
		t.Fatalf("delete code = %d; stdout = %s; stderr = %s", code, &out, &diagnostics)
	}
	if _, err := os.Stat(metadata); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata was not deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".badmeta-quarantine")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete created quarantine state: %v", err)
	}
}

func TestCancelledCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diagnostics bytes.Buffer
	if code := Run(ctx, []string{"scan", t.TempDir()}, &out, &diagnostics); code != 130 {
		t.Fatalf("code = %d; stderr = %s", code, &diagnostics)
	}
	if out.Len() != 0 {
		t.Fatalf("cancelled command wrote stdout: %s", &out)
	}
}

func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"clean", "--help"}, {"--version"}} {
		var out, diagnostics bytes.Buffer
		if code := Run(context.Background(), args, &out, &diagnostics); code != 0 || out.Len() == 0 || diagnostics.Len() != 0 {
			t.Fatalf("args = %v; code = %d; stdout = %s; stderr = %s", args, code, &out, &diagnostics)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestOutputFailure(t *testing.T) {
	var diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, failingWriter{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "Error writing output") {
		t.Fatalf("code = %d; stderr = %s", code, &diagnostics)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
