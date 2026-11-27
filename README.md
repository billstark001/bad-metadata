# Bad Metadata

Bad Metadata (`badmeta`) previews and cleans macOS metadata on Windows and macOS. An English command-line interface and a native MyGo desktop interface share the same cleaning engine. The desktop interface supports English and Simplified Chinese; it starts no browser or webview.

The application icon is embedded in both Windows executables and the native desktop window, and included in macOS application bundles.

Version: **0.1.0**. Supported platforms: Windows and macOS, Intel/AMD 64-bit and ARM64. Go 1.27 builds require Windows 10 or later or macOS 13 or later. Scheduled cleanup and new-device triggers are not implemented in this release.

## What it finds

| Type ID | Match | Default |
| --- | --- | --- |
| `appledouble` | `._*` regular files with a valid AppleDouble magic number and version | On |
| `dsstore` | `.DS_Store` regular files | On |
| `spotlight` | `.Spotlight-V100` directories and their contents | On |
| `fsevents` | `.fseventsd` directories and their contents | On |
| `trashes` | `.Trashes` and `.Trash` directories and their contents | Off |

Matches use exact metadata names. A user file named `._notes.txt` without an AppleDouble header is kept. Names such as `.DS_Store.backup` are kept. A recognized metadata directory whose category is disabled is skipped entirely, including nested metadata; leaving Trash disabled protects its complete contents. Symlinks and Windows reparse points, including junctions, are skipped. The quarantine directory is excluded from scans. Permission or traversal errors are reported rather than silently treated as a clean scan.

AppleDouble files can carry resource forks and extended attributes, including Finder information. Cleaning them can discard meaningful metadata. Review the preview and keep the quarantine until you are satisfied. Active macOS services may recreate metadata after cleanup.

## Run

Extract the matching platform ZIP. On Windows, open `badmeta-gui.exe` for the desktop interface or run `badmeta.exe` from a terminal. On macOS, open `Bad Metadata.app`; the standalone `badmeta` file is the CLI. Archives are unsigned; macOS may require approval in System Settings before opening the application.

Add the CLI's directory to your `PATH` to use `badmeta` as a short command. CLI examples below work on both platforms; replace the example root with a directory or mounted volume. Use `badmeta --help` for the complete option list.

```sh
# Read-only preview. No files are changed.
badmeta scan /Volumes/External
badmeta /Volumes/External

# Include Trash, which is excluded by default.
badmeta scan /Volumes/External --trashes

# Select individual categories.
badmeta scan /Volumes/External --types appledouble,dsstore

# JSON preview for scripts or saved reports.
badmeta scan /Volumes/External --json

# Force progress on stderr while keeping stdout machine-readable.
badmeta scan /Volumes/External --json --progress always

# Move matches to a restorable quarantine on the same volume.
badmeta clean /Volumes/External --yes

# Restore the session printed by the preceding command.
badmeta restore /Volumes/External/.badmeta-quarantine/SESSION --yes

# Explicit permanent deletion. There is no restore for this mode.
badmeta clean /Volumes/External --mode delete --yes

badmeta --version
```

Windows example:

```powershell
.\badmeta.exe scan 'L:\' --json
.\badmeta.exe clean 'L:\some-folder' --mode quarantine --yes
```

`clean` uses quarantine unless you explicitly select `--mode delete`. Mutating CLI commands require `--yes`. Without that flag, `clean` prints its preview and leaves files in place. Exit codes are `0` for success, `1` for operational errors or reported issues, `2` for invalid usage, and `130` for cancellation. Press Ctrl+C to cancel. Operations already completed remain completed, and a partial quarantine session remains available for restoration.

`--progress auto|always|never` controls progress reporting to stderr; `auto` is the default and detects an interactive terminal. Scans report visited entries and matches while the total is still unknown. Cleanup and restore report attempted items against their known totals, with verification bytes while content hashes are checked. Progress never changes the JSON format on stdout.

## Desktop workflow

1. Select English or Simplified Chinese, enter a root directory or choose it with **Browse**, and select metadata categories. Trash is opt-in.
2. Preview the matching paths, categories, sizes, and scan issues. You can export the preview as JSON.
3. Choose quarantine or permanent delete, then confirm the displayed action. Changing the root or categories requires a new preview.
4. Use **Restore** and choose a quarantine session directory to return its contents. Restoration never overwrites an existing destination.

Scanning and cleanup run in the background. **Cancel** stops further work; closing the window cancels active work. The CLI and GUI use the same detection rules, quarantine format, restore logic, and cancellation behavior.

The native progress bar is indeterminate while scanning discovers an unknown number of entries. Cleanup and restore show item counts and determinate progress, with the current path and content-verification bytes. Full completion is displayed only after the operation finishes successfully; inspect the result list for any skipped items or issues.

## Quarantine and restore

Each quarantine session is stored below the scanned root:

```text
.badmeta-quarantine/
  SESSION/
    manifest.json
    data/
      <original relative paths>
```

Quarantine moves files on the same filesystem instead of permanently removing them. Retain the complete session directory, including its manifest. Restoration uses the saved root and original relative paths and refuses destination conflicts. A conflict or access error leaves the affected item in quarantine for a later retry. Do not edit manifests or manually merge sessions.

Scans describe the filesystem at a point in time. The engine rechecks candidate contents and layout before cleaning; changed contents or entries that cannot be safely handled are reported. Timestamp changes alone do not invalidate a preview. Stop applications that write into a target while cleaning it. Use quarantine when working with valuable data. Metadata cleaning is not a substitute for a backup.

## Build and verify

Use a current **Go 1.27** installation and GNU Make. CI selects `1.27.x`, so it follows patch updates. There is no `toolchain` directive pinning a Go patch. MyGo v0.10.0 declares Go 1.27.3 as its upstream minimum, which Go records in `go.mod` when resolving the module graph.

No C compiler or frontend toolchain is needed for normal builds. Race detection needs a C compiler and uses `CGO_ENABLED=1` only for that check. On Windows, GNU Make may be named `mingw32-make`.

| Command | Action |
| --- | --- |
| `make fmt` | Format Go source and task scripts |
| `make fmt-check` | Fail if any Go source needs formatting |
| `make vet` | Run Go static checks |
| `make test` | Run tests |
| `make race` | Run tests with the race detector |
| `make version-check` | Validate SemVer and matching changelog notes |
| `make check` | Run formatting, version, vet, and test checks |
| `make build` | Build CLI and native GUI in `bin/` for the current platform |
| `make all` | Check and build |
| `make run` | Display CLI help from source |
| `make gui` | Run the native desktop interface from source |
| `make package` | Build Windows/macOS ZIPs for amd64 and arm64 in `dist/` |
| `make resources` | Regenerate platform icons and embedded Windows resources |
| `make clean` | Remove generated `bin/` and `dist/` directories |

The portable task runner also works without Make:

```sh
go run ./scripts/tasks.go fmt-check
go run ./scripts/tasks.go version-check
go vet ./...
go test ./...
go run ./scripts/tasks.go build
go run ./scripts/tasks.go resources
go run ./scripts/tasks.go package windows
go run ./scripts/tasks.go package darwin arm64
```

Packages contain documentation and both interfaces. macOS packages contain an application bundle with an executable mode preserved in the ZIP. `dist/SHA256SUMS` covers the archives generated by the latest packaging invocation. Cross-compilation verifies compilation; executing or signing a macOS application requires macOS.

`assets/icon.png` is the source icon; its generation prompt is retained in `assets/icon-prompt.txt`. `make resources` resizes that image without changing its design and writes a multi-resolution ICO, a PNG-based ICNS with Retina representations, and Windows resources for both executables and architectures. Resource generation uses the pinned `go-winres` v0.3.3 tool. Generated icons and `.syso` files are committed so normal builds need no resource compiler; CI regenerates them and checks for differences. Windows resources include version information, per-monitor DPI awareness, and long-path support. Regenerate resources after changing the source icon or release version.

To test on an actual Windows volume without touching existing files:

```powershell
pwsh -NoProfile -File scripts/test-on-volume.ps1 -Volume 'L:\'
```

The script creates a unique `badmeta-test-*` directory, previews synthetic metadata with and without Trash, quarantines and restores it, verifies SHA-256 hashes of every original fixture file, and retains the fixture and JSON reports. It never requests permanent deletion. A read-only scan of existing volume data can be performed separately with `badmeta scan 'L:\' --json`.

## Layout and project rules

`internal/cleaner` owns filesystem behavior. `internal/cli` and `internal/gui` adapt that behavior to their interfaces. `cmd/badmeta` and `cmd/badmeta-gui` are small entry points. `internal/version/VERSION` is the release version source; `scripts/tasks.go` owns portable build and packaging tasks.

See [CONTRIBUTING.md](CONTRIBUTING.md) for Conventional Commits, branch naming, SemVer, CI, and the manual release workflow. CI checks Windows and macOS, runs race detection, and cross-builds both architectures. The initial project is a local Git repository; configure a remote before using hosted CI or publishing releases.

Licensed under the [MIT License](LICENSE).
