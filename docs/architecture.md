# Architecture

The application has three behavior layers, with small command entry points:

- `internal/cleaner`: classification, safe traversal, immutable scan descriptions, execution, quarantine journaling, and restoration. It has no GUI dependency.
- `internal/cli`: English argument parsing, text/JSON output, process exit codes, and signal cancellation.
- `internal/gui`: native MyGo controls, English/Simplified Chinese strings, background operations, previews, confirmation, and cancellation. It uses `ui.View`; no web frontend is created.

Frontends select the same metadata categories and call the same scan, execute, and restore API. A future scheduler or volume-arrival adapter should call this API as a fourth adapter; it should not reimplement matching or filesystem operations.

The core's optional progress callback reports phases, current paths, visited or attempted entries, discovered matches, and verification bytes. A zero total means the work is still being discovered. Both frontends adapt those same events: the CLI writes progress to stderr, and the GUI updates native progress controls on the window thread.

## Operation lifecycle

The user selects a root and categories. Scanning returns candidates and any errors. A frontend can show or export that description without changing files. Quarantine or permanent delete is an explicit subsequent action. The engine validates candidates again at execution time and records successful items and failures separately. Cancellation stops further work; it does not pretend to roll back operations already completed.

Quarantine is a same-volume move into a reserved directory, with a manifest linking saved entries to original relative paths. Restoration refuses to overwrite existing paths. Session contents and manifests must remain together. Permanently deleting metadata does not create a restore session.

Links, junctions, reserved quarantine storage, and inaccessible paths need explicit handling. A cleanup rule must describe a precise filename/type signature and should prefer false negatives over removing unrelated user files. New rules need tests covering similarly named user files and traversal boundaries.

## Release artifacts

One version file controls CLI/UI version display and package metadata. Go 1.27 patch updates are followed by CI instead of fixed in a `toolchain` directive. Native MyGo currently contributes a minimum patch requirement through its own module declaration. Dependencies are tracked by `go.mod` and authenticated by `go.sum`.

CI builds CLI and GUI for Windows/macOS on amd64/arm64. ZIP packaging preserves executable modes and supplies a macOS application bundle. Artifact hashes travel with releases. Signing and notarization are future release infrastructure; the initial artifacts are unsigned.

`assets` embeds the source PNG for native windows. `scripts/icon.go` creates ICO and ICNS representations using deterministic resizing and container encoding. The pinned Windows resource tool wraps the ICO, manifest, and version metadata in architecture-specific COFF resource objects beside each command. CI regenerates and compares those tracked outputs. macOS packaging copies the ICNS into the application bundle and declares it in `Info.plist`.
