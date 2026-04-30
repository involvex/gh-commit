# AGENTS.md — Quick context for working on gh-commit

## What this repo does
Go-based GitHub CLI extension that stages all changes and generates conventional commit messages via the Kilo AI Gateway (`kilo-auto/free`). Single binary (`main.go`), no package subdirectories.

## Key commands
- Build: `go build -o gh-commit .`
- Install locally: `gh extension install . --force`
- Run (in a test repo): `gh commit`  
  Requires `KILO_API_KEY` env var.

## Extension install boundaries
GitHub CLI extensions must live in `~/.config/gh/extensions/` (or OS equivalent) as a folder named `involvex/gh-commit` when installed via `gh extension install`. The repo root is the extension source; building produces a single executable `gh-commit` (or `gh-commit.exe`) which the CLI invokes.

## Required environment
- `KILO_API_KEY` — must be set; extension exits immediately if missing.
- `gh` CLI — authenticated and available in PATH.
- `git` — available in PATH (normal git repo context expected).

## Build and release flow
- `go build -o gh-commit .` produces the binary.
- Releases are automated: push a tag `v*` → GitHub Actions runs `cli/gh-extension-precompile@v2` to build multi-platform binaries and publish the extension. No manual cross-build needed for releases.
- Testing locally: run the built binary in a real repo with `KILO_API_KEY` set.

## Repo-specific quirks
- No test suite or lint config in repo — validation is manual/integration.
- `git add .` is automatic and unconditional; commit only proceeds if staged diff is non-empty.
- Commit message generation: POST to Kilo API with system prompt enforcing Conventional Commits. If API fails, falls back to manual message entry (no hard failure).
- Raw terminal mode (`term.MakeRaw`) for single-char prompts; falls back to line-based input on failure (works across platforms).
- `go.mod` pins Go 1.26.2 and uses `cli/go-gh/v2` for GitHub interactions.

## Files that matter
- `main.go` — entire extension logic (single package).
- `.github/workflows/release.yml` — automated release on tag push.
- `go.mod`, `go.sum` — dependencies.

## Gotchas for agents
- Do not expect conventional unit tests; verify behavior in a real repo with `KILO_API_KEY`.
- Avoid renaming `main.go` or splitting packages without adjusting Go module and build expectations.
- Extension install paths matter when testing installed behavior vs running the binary directly.
- Commit message must be a single line (subject only); the code strips newlines and backticks from API responses.
- API failures are non-fatal (fallback to manual entry). Network/auth issues should not block user commits.