# Contributing

Use the current Go 1.27 patch release and GNU Make. MyGo currently sets a minimum Go patch in the module graph; the project does not pin an executing toolchain. See the README for commands without Make.

## Workflow

1. Branch from `main`: `feat/short-description`, `fix/short-description`, or `chore/short-description`.
2. Keep cleanup rules and filesystem behavior in `internal/cleaner`; both frontends call that API.
3. Add focused regression tests for behavior changes, especially boundary checks, links, changed files, and restoration conflicts.
4. Run `make check` and `make build`. Run `make race` when changing concurrent behavior.
5. Open a pull request with a Conventional Commit title and explain behavior and validation.

Use commits such as `feat(cleaner): add a metadata rule`, `fix(gui): cancel work on close`, or `docs: explain restore conflicts`. Allowed types are `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, and `revert`. Add `!` for a breaking change, with a `BREAKING CHANGE:` footer. Use an imperative English subject without a trailing period. The optional local hook runs the same title convention:

```sh
git config core.hooksPath .githooks
```

Commit history is local until a remote is configured. Repository maintainers should require the `Quality` and platform CI jobs before merging and use squash merges so the PR title becomes the commit subject.

## Versioning and release

`internal/version/VERSION` is the source of truth. Start at `0.1.0`; use `MAJOR.MINOR.PATCH` with no leading `v` in the file. Pre-1.0 minor releases may change APIs; patch releases fix compatible behavior. Update the version file and add a dated changelog entry together, run `make resources`, and commit the regenerated Windows resource files with `chore(release): prepare 0.2.0` or the appropriate release number. Changes to `assets/icon.png` also require `make resources` and committed ICO, ICNS, and `.syso` updates.

After checks pass on `main`, run the manual **Release** workflow. It rebuilds and tests the exact revision, checks the matching changelog section, creates or verifies `vMAJOR.MINOR.PATCH`, and publishes platform ZIP files, SHA-256 checksums, and release notes. Release tags must never move to a different revision. The workflow requires the repository's Actions token to have release permissions; no registry or signing credentials are needed.

Archives are unsigned. Signing and notarizing macOS bundles requires a macOS signing identity and is outside the initial release. Only Windows and macOS are supported. Scheduled or hardware-triggered cleanup is reserved for later versions.
