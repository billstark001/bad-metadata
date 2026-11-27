// Package cli provides the English command-line interface to the shared cleaner.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/billstark001/bad-metadata/internal/cleaner"
	"github.com/billstark001/bad-metadata/internal/version"
)

const usage = `badmeta - preview and clean macOS metadata

Usage:
  badmeta [scan] [options] PATH
  badmeta clean [options] PATH
  badmeta restore [options] SESSION
  badmeta --version

Commands:
  scan       Preview matching metadata; never changes files (default).
  clean      Scan, then quarantine or permanently delete matching metadata.
             Without --yes, only a preview is produced.
  restore    Restore a quarantine session. Requires --yes to change files.

Options:
  --types LIST   Comma-separated categories: appledouble, dsstore,
                 spotlight, fsevents, trashes. Defaults to all except trashes.
  --trashes      Also include macOS trash. It may contain recoverable user files.
  --mode MODE    quarantine (default) or delete; valid with clean only.
  --yes, -y      Apply clean or restore. Delete mode is permanent.
  --json        Write the complete plan or result as JSON to standard output.
  --progress MODE
                Progress on stderr: auto (terminal only), always, or never.
  --help, -h    Show this help.
  --version     Show the application version.
  --            Treat following arguments as literal paths.

Options may appear before or after PATH. Use one root directory per command.
Quarantine sessions stay on the same volume under .badmeta-quarantine.
Press Ctrl+C to cancel; a partially completed quarantine remains restorable.

Examples:
  badmeta L:\
  badmeta scan --trashes --json L:\ > preview.json
  badmeta clean L:\ --yes
  badmeta clean --mode delete --yes /Volumes/USB
  badmeta restore "L:\.badmeta-quarantine\SESSION" --yes
`

type arguments struct {
	command  string
	path     string
	kinds    []cleaner.Kind
	mode     cleaner.Mode
	yes      bool
	json     bool
	help     bool
	version  bool
	progress string
}

// Run executes a command and returns a process exit code. Output and diagnostics
// have separate writers so that JSON on stdout remains machine-readable.
// Exit codes are 0 for success, 1 for operational errors or partial results,
// 2 for invalid usage, and 130 for cancellation.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\nRun badmeta --help for usage.\n", err)
		return 2
	}
	if opts.help {
		if _, err := io.WriteString(stdout, usage); err != nil {
			return outputError(stderr, err)
		}
		return 0
	}
	if opts.version {
		if _, err := fmt.Fprintf(stdout, "badmeta %s\n", version.Number()); err != nil {
			return outputError(stderr, err)
		}
		return 0
	}
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(stderr, "Cancelled.")
		return 130
	}
	ctx = progressContext(ctx, opts.progress, stderr)
	if opts.command == "restore" {
		if !opts.yes {
			fmt.Fprintln(stderr, "No changes made. Add --yes to restore this quarantine session.")
			return 2
		}
		result, err := cleaner.Restore(ctx, opts.path)
		return writeResult(ctx, opts, result, err, stdout, stderr)
	}

	plan, err := cleaner.Scan(ctx, opts.path, cleaner.Options{Kinds: opts.kinds})
	if err != nil {
		return operationError(ctx, stderr, err)
	}
	if opts.command == "scan" || !opts.yes {
		if opts.json {
			err = cleaner.WriteJSON(stdout, plan)
		} else {
			err = writePlan(stdout, plan)
		}
		if err != nil {
			return outputError(stderr, err)
		}
		if opts.command == "clean" {
			fmt.Fprintln(stderr, "Preview only. No changes made. Add --yes to apply this cleanup.")
		}
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "Cancelled.")
			return 130
		}
		if len(plan.Issues) != 0 {
			if opts.json {
				fmt.Fprintf(stderr, "Scan completed with %d issue(s); see JSON for details.\n", len(plan.Issues))
			}
			return 1
		}
		return 0
	}

	if !opts.json {
		if err := writePlan(stdout, plan); err != nil {
			return outputError(stderr, err)
		}
	}
	result, err := cleaner.Execute(ctx, plan, opts.mode)
	result.Issues = append(result.Issues, plan.Issues...)
	return writeResult(ctx, opts, result, err, stdout, stderr)
}

func writePlan(w io.Writer, plan cleaner.Plan) error {
	if _, err := fmt.Fprintf(w, "Preview: %s\n", plan.Root); err != nil {
		return err
	}
	for _, item := range plan.Items {
		if _, err := fmt.Fprintf(w, "  [%s] %q (%d bytes)\n", item.Kind, item.Path, item.Bytes); err != nil {
			return err
		}
	}
	for _, issue := range plan.Issues {
		if _, err := fmt.Fprintf(w, "  Issue: %q: %s\n", issue.Path, issue.Error); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "%d candidate(s), %d bytes, %d issue(s). No changes made by preview.\n", len(plan.Items), plan.Bytes, len(plan.Issues))
	return err
}

func writeResult(ctx context.Context, opts arguments, result cleaner.Result, operationErr error, stdout, stderr io.Writer) int {
	var err error
	if opts.json {
		err = cleaner.WriteJSON(stdout, result)
	} else {
		for _, path := range result.Completed {
			if _, err = fmt.Fprintf(stdout, "  Processed: %q\n", path); err != nil {
				break
			}
		}
		if err == nil && result.Session != "" {
			_, err = fmt.Fprintf(stdout, "Quarantine session: %s\n", result.Session)
		}
		if err == nil {
			for _, issue := range result.Issues {
				if _, err = fmt.Fprintf(stdout, "  Issue: %q: %s\n", issue.Path, issue.Error); err != nil {
					break
				}
			}
		}
		if err == nil {
			_, err = fmt.Fprintf(stdout, "%d item(s) processed, %d issue(s).\n", len(result.Completed), len(result.Issues))
		}
	}
	if err != nil {
		return outputError(stderr, err)
	}
	if result.Cancelled || ctx.Err() != nil || errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) {
		fmt.Fprintln(stderr, "Cancelled. Completed changes are listed above or in the JSON result.")
		return 130
	}
	if operationErr != nil {
		return operationError(ctx, stderr, operationErr)
	}
	if len(result.Issues) != 0 {
		if opts.json {
			fmt.Fprintf(stderr, "Operation completed with %d issue(s); see JSON for details.\n", len(result.Issues))
		}
		return 1
	}
	return 0
}

func operationError(ctx context.Context, stderr io.Writer, err error) int {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(stderr, "Cancelled.")
		return 130
	}
	fmt.Fprintf(stderr, "Error: %v\n", err)
	return 1
}

func outputError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "Error writing output: %v\n", err)
	return 1
}

func parse(args []string) (arguments, error) {
	opts := arguments{command: "scan", kinds: cleaner.DefaultKinds(), mode: cleaner.Quarantine, progress: "auto"}
	if len(args) == 0 {
		opts.help = true
		return opts, nil
	}
	if args[0] == "scan" || args[0] == "clean" || args[0] == "restore" {
		opts.command, args = args[0], args[1:]
	}
	var paths []string
	var typesValue string
	var typesSet, modeSet, progressSet, trashes bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--help", "-h":
			if hasValue {
				return opts, fmt.Errorf("%s does not take a value", name)
			}
			opts.help = true
		case "--version":
			if hasValue {
				return opts, fmt.Errorf("%s does not take a value", name)
			}
			opts.version = true
		case "--json", "--yes", "-y", "--trashes":
			if hasValue {
				return opts, fmt.Errorf("%s does not take a value", name)
			}
			switch name {
			case "--json":
				opts.json = true
			case "--yes", "-y":
				opts.yes = true
			case "--trashes":
				trashes = true
			}
		case "--types", "--mode", "--progress":
			if !hasValue {
				i++
				if i == len(args) || strings.HasPrefix(args[i], "--") {
					return opts, fmt.Errorf("%s requires a value", name)
				}
				value = args[i]
			}
			if name == "--types" {
				if typesSet {
					return opts, errors.New("--types may only be specified once")
				}
				typesSet, typesValue = true, value
			} else if name == "--mode" {
				if modeSet {
					return opts, errors.New("--mode may only be specified once")
				}
				modeSet, opts.mode = true, cleaner.Mode(value)
			} else {
				if progressSet {
					return opts, errors.New("--progress may only be specified once")
				}
				progressSet, opts.progress = true, value
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown option %q", arg)
			}
			paths = append(paths, arg)
		}
	}
	if opts.help || opts.version {
		return opts, nil
	}
	if len(paths) != 1 || paths[0] == "" {
		return opts, errors.New("exactly one directory path is required")
	}
	opts.path = paths[0]
	if opts.progress != "auto" && opts.progress != "always" && opts.progress != "never" {
		return opts, fmt.Errorf("unknown progress mode %q; expected auto, always, or never", opts.progress)
	}
	if modeSet && opts.command != "clean" {
		return opts, errors.New("--mode is only valid with clean")
	}
	if opts.mode != cleaner.Quarantine && opts.mode != cleaner.Delete {
		return opts, fmt.Errorf("unknown mode %q; expected quarantine or delete", opts.mode)
	}
	if opts.yes && opts.command == "scan" {
		return opts, errors.New("--yes is only valid with clean or restore")
	}
	if opts.command == "restore" && (typesSet || trashes) {
		return opts, errors.New("--types and --trashes are not valid with restore")
	}
	if typesSet {
		opts.kinds = nil
		for _, value := range strings.Split(typesValue, ",") {
			kind := cleaner.Kind(strings.TrimSpace(value))
			switch kind {
			case cleaner.AppleDouble, cleaner.DSStore, cleaner.Spotlight, cleaner.FSEvents, cleaner.Trashes:
				opts.kinds = appendKind(opts.kinds, kind)
			default:
				return opts, fmt.Errorf("unknown metadata category %q", value)
			}
		}
	}
	if trashes {
		opts.kinds = appendKind(opts.kinds, cleaner.Trashes)
	}
	return opts, nil
}

func appendKind(kinds []cleaner.Kind, kind cleaner.Kind) []cleaner.Kind {
	for _, present := range kinds {
		if present == kind {
			return kinds
		}
	}
	return append(kinds, kind)
}
