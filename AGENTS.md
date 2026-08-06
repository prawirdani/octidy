# AGENTS.md

Interactive TUI (Bubble Tea) for deleting opencode sessions. Thin wrapper over the
`opencode` CLI — no opencode storage internals touched.

## Commands
- `make build` — build for current OS into `dist/octidy`
- `make build-all` — cross-compile all OS/arch into `dist/`
- `make test` / `make test-race` — run tests (race detector required for the fan-out/backfill concurrency paths)
- `make lint` — golangci-lint, falls back to `go vet` if not installed
- `make clean` — remove `dist/`

## CI
- `.github/workflows/ci.yml`: test job (lint + race tests) → build matrix → on `v*` tags, publish
  prebuilt binaries as release assets (`softprops/action-gh-release`).

## Key facts
- Loads sessions via `opencode session list --format json`, deletes via `opencode session delete <id>`.
- `OPENCODE_BIN` env var overrides the opencode executable (opencode.go:12).
- Source layout: opencode.go = data access, model.go = state machine, view.go = rendering,
  styles.go = lipgloss theme, util.go = pure helpers.
- Tests never need a real opencode install: `model.deleter` is injected (model.go:63) and
  `opencodeBin` is pointed at a fake script.
- `maxConcurrent = 8` caps parallel deletes (model.go:31); opencode's store is single-writer SQLite.
- Headless test pattern: `tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(...))` + injected
  deleter, so no TTY needed.
