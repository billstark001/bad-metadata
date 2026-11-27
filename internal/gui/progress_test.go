package gui

import (
	"context"
	"testing"

	"github.com/billstark001/bad-metadata/internal/cleaner"
	"github.com/egoist/mygo/ui"
)

func TestScanProgressIsVisibleAndIndeterminateWithLocalizedCounts(t *testing.T) {
	a := newApp(t.TempDir())
	a.language = "简体中文"
	a.busy, a.hasProgress = true, true
	a.cancel = func() {}
	a.progress = cleaner.Progress{Operation: "scan", Phase: "walking", Path: "folder/file.txt", Done: 17, Matches: 3}
	tt := ui.NewTester(a.view, 1040, 780)
	bar, ok := tt.AccessByRole(ui.RoleProgress, "操作进度")
	if !ok || bar.Bounds.W <= 0 || bar.Bounds.H <= 0 || bar.Now >= bar.Min {
		t.Fatalf("scan must show a visible native indeterminate bar: %+v, found %v", bar, ok)
	}
	for _, label := range []string{"正在扫描文件…", "已扫描 17 项 · 匹配 3 项", "folder/file.txt"} {
		if !tt.HasText(label) {
			t.Fatalf("missing localized progress label %q", label)
		}
	}
}

func TestCleanupProgressShowsAttemptCountsAndVerificationBytes(t *testing.T) {
	a := newApp(t.TempDir())
	a.busy, a.hasProgress = true, true
	a.cancel = func() {}
	a.progress = cleaner.Progress{
		Operation: "clean", Phase: "verifying", Path: ".Spotlight-V100",
		Done: 1, Total: 4, BytesDone: 1024, BytesTotal: 4096,
	}
	tt := ui.NewTester(a.view, 1040, 780)
	bar, ok := tt.AccessByRole(ui.RoleProgress, "Operation progress")
	if !ok || bar.Now != 0.25 || bar.Bounds.H <= 0 {
		t.Fatalf("cleanup progress = %+v, found %v", bar, ok)
	}
	for _, label := range []string{"Verifying contents…", "1 / 4 items · 25%", "Verified 1.0 KiB / 4.0 KiB", ".Spotlight-V100"} {
		if !tt.HasText(label) {
			t.Fatalf("missing progress label %q", label)
		}
	}
}

func TestOnlySuccessfulCompletionShowsFullProgress(t *testing.T) {
	a := newApp(t.TempDir())
	a.busy, a.hasProgress = true, true
	a.cancel = func() {}
	a.progress = cleaner.Progress{Operation: "restore", Phase: "restoring", Done: 4, Total: 4}
	tt := ui.NewTester(a.view, 1040, 780)
	if bar, _ := tt.AccessByRole(ui.RoleProgress, "Operation progress"); bar.Now >= 1 {
		t.Fatal("the pending journal update must not show full completion")
	}
	a.finish(context.Canceled, true)
	tt.Frame()
	if bar, _ := tt.AccessByRole(ui.RoleProgress, "Operation progress"); bar.Now >= 1 {
		t.Fatal("a cancelled operation must not show full completion")
	}
	if !tt.HasText("Cancelled") {
		t.Fatal("cancelled progress lost its phase label")
	}
	a.finish(nil, false)
	tt.Frame()
	if bar, _ := tt.AccessByRole(ui.RoleProgress, "Operation progress"); bar.Now != 1 {
		t.Fatalf("successful completion must fill the progress bar: %+v", bar)
	}
	if !tt.HasText("4 / 4 items · 100%") {
		t.Fatal("completed progress must show the final counts")
	}
}

func TestProgressAndRestoreControlsFitMinimumWindow(t *testing.T) {
	for _, language := range []string{"English", "简体中文"} {
		t.Run(language, func(t *testing.T) {
			a := newApp(t.TempDir())
			a.language = language
			a.busy, a.hasProgress = true, true
			a.cancel = func() {}
			a.progress = cleaner.Progress{
				Operation: "clean", Phase: "verifying",
				Path: "very-long-folder-name/subfolder/another-subfolder/.Spotlight-V100/index",
				Done: 1, Total: 4, BytesDone: 1024, BytesTotal: 4096,
			}
			a.rows = []activity{{status: "dsstore", path: ".DS_Store", detail: "32 B"}}
			tt := ui.NewTester(a.view, 860, 680)
			for _, control := range []struct {
				role ui.Role
				name string
			}{
				{ui.RoleProgress, a.text("progress")},
				{ui.RoleTextField, a.text("session")},
				{ui.RoleButton, a.text("restore")},
			} {
				item, ok := tt.AccessByRole(control.role, control.name)
				minimumHeight := float32(24)
				if control.role == ui.RoleProgress {
					minimumHeight = 8
				}
				if !ok || item.Bounds.W <= 0 || item.Bounds.H < minimumHeight || item.Bounds.Y+item.Bounds.H > 680 {
					t.Fatalf("control %q is clipped at the minimum window size: %+v, found %v", control.name, item.Bounds, ok)
				}
			}
			if hint, ok := tt.Find(a.text("restoreHint")); !ok || hint.H <= 0 || hint.Y+hint.H > 680 {
				t.Fatalf("bottom restore hint is clipped: %+v, found %v", hint, ok)
			}
		})
	}
}
