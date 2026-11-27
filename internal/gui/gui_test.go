package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/billstark001/bad-metadata/internal/cleaner"
	"github.com/egoist/mygo/ui"
)

func TestViewStartsWithRecoverableCleanupAndOptionalTrashes(t *testing.T) {
	a := newApp(t.TempDir())
	tt := ui.NewTester(a.view, 1040, 780)
	if a.mode != cleaner.Quarantine {
		t.Fatal("cleanup must default to recoverable quarantine")
	}
	checkbox, ok := tt.AccessByRole(ui.RoleCheckBox, "Trashes (optional)")
	if !ok || checkbox.Checked {
		t.Fatal("Trashes must be visible and unchecked by default")
	}
	button, ok := tt.AccessByRole(ui.RoleButton, "Clean previewed items…")
	if !ok || !button.Disabled {
		t.Fatal("cleanup must be disabled without a preview")
	}
}

func TestCategoryChangeInvalidatesPreview(t *testing.T) {
	a := newApp(t.TempDir())
	a.plan = &cleaner.Plan{Items: []cleaner.Item{{Path: ".DS_Store", Kind: cleaner.DSStore}}}
	tt := ui.NewTester(a.view, 1040, 780)
	if err := tt.Click("Trashes (optional)"); err != nil {
		t.Fatal(err)
	}
	if a.plan != nil {
		t.Fatal("an old preview remains executable after a category change")
	}
	button, ok := tt.AccessByRole(ui.RoleButton, "Clean previewed items…")
	if !ok || !button.Disabled || !tt.HasText(a.text("stale")) {
		t.Fatal("settings changes must require another preview")
	}
}

func TestEditingRootInvalidatesPreview(t *testing.T) {
	a := newApp(t.TempDir())
	a.plan = &cleaner.Plan{Items: []cleaner.Item{{Path: ".DS_Store", Kind: cleaner.DSStore}}}
	tt := ui.NewTester(a.view, 1040, 780)
	field, ok := tt.AccessByRole(ui.RoleTextField, "Folder to scan")
	if !ok {
		t.Fatal("missing root input")
	}
	r := field.Bounds
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Type("another-folder")
	if a.plan != nil {
		t.Fatal("editing the root must invalidate the old preview")
	}
}

func TestLanguageSwitchTranslatesControls(t *testing.T) {
	a := newApp(t.TempDir())
	tt := ui.NewTester(a.view, 1040, 780)
	if err := tt.Click("English"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	if a.language != "简体中文" || !tt.HasText("扫描文件夹") || !tt.HasText("恢复…") {
		t.Fatal("language selection did not translate the interface")
	}
}

func TestCompletedResultRemainsExportableWithIssues(t *testing.T) {
	a := newApp(t.TempDir())
	result := cleaner.Result{
		Session:   filepath.Join(a.root, ".badmeta-quarantine", "session"),
		Completed: []string{".DS_Store"},
		Issues:    []cleaner.Issue{{Path: "blocked", Error: "permission denied"}},
		Cancelled: true,
	}
	a.showResult(result, nil, string(cleaner.Quarantine))
	tt := ui.NewTester(a.view, 1040, 780)
	button, ok := tt.AccessByRole(ui.RoleButton, "Export report…")
	if !ok || button.Disabled || a.plan != nil {
		t.Fatal("the completed result must remain exportable without a preview")
	}
	path := filepath.Join(t.TempDir(), "result.json")
	if err := saveReport(path, *a.result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded cleaner.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("export lost operation details: got %+v, want %+v", decoded, result)
	}
}
