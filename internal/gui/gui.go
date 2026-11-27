// Package gui is a native MyGo frontend for the shared metadata cleaner.
package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/billstark001/bad-metadata/assets"
	"github.com/billstark001/bad-metadata/internal/cleaner"
	"github.com/billstark001/bad-metadata/internal/version"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type category struct {
	kind    cleaner.Kind
	enabled bool
}

type activity struct {
	status string
	path   string
	detail string
}

// app's state is owned by the main thread. Workers capture their inputs and
// deliver their results with Window.Update; they never retain ui.Element values.
type app struct {
	window           *mygo.Window
	root             string
	session          string
	language         string
	setLocale        func(string)
	categories       []category
	mode             cleaner.Mode
	plan             *cleaner.Plan
	result           *cleaner.Result
	rows             []activity
	list             ui.ListState
	busy             bool
	status           string
	lastError        string
	cancel           context.CancelFunc
	closed           bool
	progress         cleaner.Progress
	hasProgress      bool
	progressComplete bool
	jobID            uint64
}

func newApp(root string) *app {
	a := &app{root: root, language: "English", mode: cleaner.Quarantine, status: "ready"}
	defaults := cleaner.DefaultKinds()
	for _, kind := range []cleaner.Kind{cleaner.AppleDouble, cleaner.DSStore, cleaner.Spotlight, cleaner.FSEvents, cleaner.Trashes} {
		enabled := false
		for _, def := range defaults {
			if kind == def {
				enabled = true
				break
			}
		}
		a.categories = append(a.categories, category{kind: kind, enabled: enabled})
	}
	return a
}

// Run starts the native UI and blocks until its last window closes.
func Run() error {
	root, err := os.Getwd()
	if err != nil {
		root = ""
	}
	a := newApp(root)
	mygo.App.SetName("Bad Metadata")
	mygo.App.SetVersion(version.Number())
	mygo.App.SetLanguages("en", "zh-CN")
	a.setLocale = mygo.App.SetLocale
	mygo.App.WhenReady(func() {
		if strings.HasPrefix(strings.ToLower(mygo.App.Locale()), "zh") {
			a.language = "简体中文"
		}
		a.window = mygo.NewWindow(mygo.WindowOptions{
			Title: "Bad Metadata", Width: 1040, Height: 780,
			MinWidth: 860, MinHeight: 680, Content: ui.View(a.view),
		})
		if err := a.window.SetIcon(assets.IconPNG); err != nil {
			a.lastError = err.Error()
		}
		a.window.OnClosed(func() {
			a.closed = true
			if a.cancel != nil {
				a.cancel()
			}
		})
	})
	return mygo.App.Run()
}

func (a *app) view(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, a.text("title")).FontSize(26).Bold()
			ui.Text(c, "v"+version.Number()).TextColor(theme.TextMuted).Grow(1)
			if ui.Select(c.Key("language"), &a.language, []string{"English", "简体中文"}).Label(a.text("language")).Width(145).Changed() && a.setLocale != nil {
				tag := "en"
				if a.language == "简体中文" {
					tag = "zh-CN"
				}
				a.setLocale(tag)
			}
		})
		ui.Text(c, a.text("subtitle")).TextColor(theme.TextMuted)
		ui.Column(c).FillWidth().Gap(8).Padding(14).Border(1, theme.Border).Radius(8).Children(func() {
			ui.Text(c, a.text("root")).Bold()
			ui.Row(c).Gap(8).Children(func() {
				if ui.TextInput(c.Key("root"), &a.root).Label(a.text("root")).Placeholder(a.text("rootPlaceholder")).Grow(1).Disabled(a.busy).Changed() {
					a.invalidatePlan()
				}
				if ui.Button(c.Key("browse-root"), a.text("browse")).Disabled(a.busy).Clicked() {
					a.browse(false)
				}
			})
			ui.Text(c, a.text("categories")).FontSize(12).TextColor(theme.TextMuted)
			ui.Row(c).Gap(16).Children(func() {
				for i := range a.categories {
					cat := &a.categories[i]
					if ui.Checkbox(c.Key(cat.kind), &cat.enabled, a.text(string(cat.kind))).Disabled(a.busy).Changed() {
						a.invalidatePlan()
					}
				}
			})
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("scan"), a.text("scan")).Disabled(a.busy || strings.TrimSpace(a.root) == "" || len(a.selectedKinds()) == 0).Clicked() {
				a.scan()
			}
			if ui.Button(c.Key("cancel"), a.text("cancel")).Disabled(!a.busy || a.cancel == nil).Clicked() {
				a.cancel()
				a.status = "canceling"
			}
			ui.Text(c, "").Grow(1)
			mode := a.modeLabel()
			if ui.Select(c.Key("mode"), &mode, []string{a.text("quarantineMode"), a.text("deleteMode")}).Label(a.text("mode")).Width(215).Disabled(a.busy).Changed() {
				a.mode = cleaner.Quarantine
				if mode == a.text("deleteMode") {
					a.mode = cleaner.Delete
				}
			}
			if ui.Button(c.Key("clean"), a.text("clean")).Disabled(a.busy || a.plan == nil || len(a.plan.Items) == 0).Clicked() {
				a.confirmClean()
			}
			if ui.Button(c.Key("export"), a.text("export")).Disabled(a.busy || a.plan == nil && a.result == nil).Clicked() {
				a.exportReport()
			}
		})
		if a.hasProgress {
			a.progressView(c)
		}
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, a.text("preview")).Bold().Grow(1)
			if a.plan != nil {
				ui.Text(c, fmt.Sprintf(a.text("summary"), len(a.plan.Items), formatBytes(a.plan.Bytes), len(a.plan.Issues))).FontSize(12).TextColor(theme.TextMuted)
			} else if a.result != nil {
				ui.Text(c, fmt.Sprintf(a.text("result"), len(a.result.Completed), len(a.result.Issues))).FontSize(12).TextColor(theme.TextMuted)
			}
		})
		if len(a.rows) == 0 {
			ui.Column(c).Grow(1).FillWidth().Center().Border(1, theme.Border).Radius(8).Children(func() {
				ui.Text(c, a.text("empty")).TextColor(theme.TextMuted)
			})
		} else {
			columns := []ui.TableColumn{{Title: a.text("type"), Width: 155}, {Title: a.text("path")}, {Title: a.text("details"), Width: 270}}
			ui.Table(c.Key("activity"), &a.list, columns, len(a.rows), func(row, col int) {
				item := a.rows[row]
				switch col {
				case 0:
					ui.Text(c, a.text(item.status)).SingleLine()
				case 1:
					ui.Text(c, item.path).SingleLine().Tooltip(item.path)
				case 2:
					ui.Text(c, item.detail).SingleLine().Tooltip(item.detail)
				}
			}).Grow(1).FillWidth().Border(1, theme.Border).Radius(8)
		}
		ui.Text(c, a.text(a.status)).FontSize(12).TextColor(theme.TextMuted)
		if a.lastError != "" {
			ui.Text(c, a.text("failed")+": "+a.lastError).FontSize(12).MaxHeight(48)
		}
		ui.Column(c).FillWidth().Gap(6).Padding(12).Border(1, theme.Border).Radius(8).Children(func() {
			ui.Text(c, a.text("session")).Bold()
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("session"), &a.session).Label(a.text("session")).Placeholder(a.text("sessionPlaceholder")).Grow(1).Disabled(a.busy)
				if ui.Button(c.Key("browse-session"), a.text("browse")).Disabled(a.busy).Clicked() {
					a.browse(true)
				}
				if ui.Button(c.Key("restore"), a.text("restore")).Disabled(a.busy || strings.TrimSpace(a.session) == "").Clicked() {
					a.confirmRestore()
				}
			})
			ui.Text(c, a.text("restoreHint")).FontSize(12).TextColor(theme.TextMuted)
		})
	})
}

func (a *app) selectedKinds() []cleaner.Kind {
	var kinds []cleaner.Kind
	for _, cat := range a.categories {
		if cat.enabled {
			kinds = append(kinds, cat.kind)
		}
	}
	return kinds
}

func (a *app) invalidatePlan() {
	a.plan, a.result = nil, nil
	a.rows = nil
	a.list = ui.ListState{}
	a.status, a.lastError = "stale", ""
	a.hasProgress, a.progressComplete = false, false
}

func (a *app) modeLabel() string {
	if a.mode == cleaner.Delete {
		return a.text("deleteMode")
	}
	return a.text("quarantineMode")
}

func (a *app) update(fn func()) {
	a.window.Update(func() {
		if !a.closed {
			fn()
		}
	})
}

func (a *app) start(status string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel, a.busy = cancel, true
	a.status, a.lastError = status, ""
	a.jobID++
	jobID := a.jobID
	a.progress = cleaner.Progress{Phase: "starting"}
	a.hasProgress, a.progressComplete = true, false
	return cleaner.WithProgress(ctx, func(progress cleaner.Progress) {
		a.update(func() {
			if a.busy && a.jobID == jobID {
				a.progress = progress
			}
		})
	})
}

func (a *app) finish(err error, cancelled bool) {
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel, a.busy = nil, false
	a.status = "done"
	a.progressComplete = err == nil && !cancelled
	if cancelled || errors.Is(err, context.Canceled) {
		a.status = "cancelled"
		a.progress.Phase = "cancelled"
	} else if err != nil {
		a.lastError = err.Error()
		a.status = "failed"
		a.progress.Phase = "failed"
	} else {
		a.progress.Phase = "done"
	}
}

func (a *app) scan() {
	root, kinds := a.root, a.selectedKinds()
	a.invalidatePlan()
	ctx := a.start("scanning")
	go func() {
		plan, err := cleaner.Scan(ctx, root, cleaner.Options{Kinds: kinds})
		a.update(func() {
			a.finish(err, false)
			if err == nil {
				a.plan = &plan
			}
			for _, item := range plan.Items {
				a.rows = append(a.rows, activity{string(item.Kind), item.Path, formatBytes(item.Bytes)})
			}
			for _, issue := range plan.Issues {
				a.rows = append(a.rows, activity{"warning", issue.Path, issue.Error})
			}
		})
	}()
}

func (a *app) browse(session bool) {
	title, initial := a.text("chooseRoot"), a.root
	if session {
		title, initial = a.text("chooseSession"), a.session
	}
	a.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: title, DefaultPath: initial, Directory: true, ShowHiddenFiles: true})
		a.update(func() {
			a.busy = false
			if err != nil {
				a.lastError = err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			if session {
				a.session = paths[0]
			} else if a.root != paths[0] {
				a.root = paths[0]
				a.invalidatePlan()
			}
		})
	}()
}

func (a *app) confirmClean() {
	if a.plan == nil || len(a.plan.Items) == 0 {
		return
	}
	plan, mode := *a.plan, a.mode
	message, detail, action := "confirmQuarantine", "quarantineDetail", "quarantineAction"
	if mode == cleaner.Delete {
		message, detail, action = "confirmDelete", "deleteDetail", "deleteAction"
	}
	options := mygo.MessageOptions{
		Parent: a.window, Type: mygo.MessageWarning, Title: a.text("title"),
		Message: fmt.Sprintf(a.text(message), len(plan.Items)), Detail: a.text(detail) + "\n\n" + plan.Root,
		Buttons: []string{a.text("cancel"), a.text(action)}, DefaultButton: 0, CancelButton: 0,
	}
	if mode == cleaner.Delete {
		options.DestructiveButtons = []int{1}
	}
	a.busy = true
	go func() {
		answer, err := mygo.Dialog.Message(options)
		a.update(func() {
			a.busy = false
			if err != nil {
				a.lastError = err.Error()
				return
			}
			if answer.Button != 1 {
				return
			}
			a.plan = nil // Never execute the same preview twice.
			ctx := a.start("cleaning")
			go func() {
				result, err := cleaner.Execute(ctx, plan, mode)
				// Scan issues remain part of the final activity and JSON report.
				result.Issues = append(append([]cleaner.Issue{}, plan.Issues...), result.Issues...)
				a.update(func() { a.showResult(result, err, string(mode)) })
			}()
		})
	}()
}

func (a *app) confirmRestore() {
	session := strings.TrimSpace(a.session)
	if session == "" {
		return
	}
	options := mygo.MessageOptions{
		Parent: a.window, Type: mygo.MessageQuestion, Title: a.text("title"),
		Message: a.text("confirmRestore"), Detail: a.text("restoreDetail") + "\n\n" + session,
		Buttons: []string{a.text("cancel"), a.text("restoreAction")}, DefaultButton: 0, CancelButton: 0,
	}
	a.busy = true
	go func() {
		answer, err := mygo.Dialog.Message(options)
		a.update(func() {
			a.busy = false
			if err != nil {
				a.lastError = err.Error()
				return
			}
			if answer.Button != 1 {
				return
			}
			a.plan = nil
			ctx := a.start("restoring")
			go func() {
				result, err := cleaner.Restore(ctx, session)
				a.update(func() { a.showResult(result, err, "restore") })
			}()
		})
	}()
}

func (a *app) showResult(result cleaner.Result, err error, operation string) {
	a.finish(err, result.Cancelled)
	a.result = &result
	a.rows, a.list = nil, ui.ListState{}
	if result.Session != "" {
		a.session = result.Session
	}
	status := "quarantined"
	if operation == string(cleaner.Delete) {
		status = "deleted"
	} else if operation == "restore" {
		status = "restored"
	}
	for _, path := range result.Completed {
		a.rows = append(a.rows, activity{status, path, ""})
	}
	for _, issue := range result.Issues {
		a.rows = append(a.rows, activity{"warning", issue.Path, issue.Error})
	}
}

func (a *app) exportReport() {
	var report any
	if a.plan != nil {
		report = *a.plan
	} else if a.result != nil {
		report = *a.result
	} else {
		return
	}
	options := mygo.SaveDialogOptions{Parent: a.window, Title: a.text("exportTitle"), DefaultPath: "badmeta-report.json", Filters: []mygo.FileFilter{{Name: a.text("json"), Extensions: []string{"json"}}}}
	a.busy = true
	go func() {
		path, err := mygo.Dialog.Save(options)
		if err == nil && path != "" {
			err = saveReport(path, report)
		}
		a.update(func() {
			a.busy = false
			if err != nil {
				a.lastError = err.Error()
			} else if path != "" {
				a.status, a.lastError = "exported", ""
			}
		})
	}()
}

func saveReport(path string, report any) error {
	// A failed encoding never truncates an existing report.
	temp, err := os.CreateTemp(filepath.Dir(path), ".badmeta-report-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := cleaner.WriteJSON(temp, report); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		if value < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return ""
}
