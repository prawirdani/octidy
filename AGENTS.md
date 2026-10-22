# AGENTS.md

Interactive TUI (Bubble Tea) for deleting opencode sessions. Thin wrapper over the
`opencode` CLI — no opencode storage internals touched.

## Rules
- NEVER commit or push without explicit user approval.

## Commands
- `make build` — build for current OS into `dist/octidy`
- `make build-all` — cross-compile all OS/arch into `dist/`
- `make test` / `make test-race` — run tests (race detector required for the fan-out/backfill concurrency paths)
- `make lint` — golangci-lint, falls back to `go vet` if not installed
- `make clean` — remove `dist/`
- `./dist/octidy --prune` — sweep orphan sessions/messages/dirs (safe while opencode runs)
- `./dist/octidy --vacuum` — `--prune` + WAL checkpoint + VACUUM (stop the service first)

## CI
- `.github/workflows/ci.yml`: test job (lint + race tests) → build matrix → on `v*` tags, publish
  prebuilt binaries as release assets (`softprops/action-gh-release`).

## Key facts
- Loads root sessions across all projects via `opencode api session.list` (v2). v1 installs fall back
  to project-scoped `opencode session list --format json`. Deletes via `opencode session delete <id>`.
- Pruning: `collectArtifacts` runs BEFORE deletes (parts that name tool-output/shell files cascade
  away), `prunePaths` removes them after the TUI exits. `--prune` sweeps orphan rows/dirs;
  `--vacuum` adds WAL checkpoint + VACUUM (needs free disk ≈ db size).
- Store access uses `modernc.org/sqlite` (pure Go, no cgo) in prune.go; read-only connections are
  safe while the background server runs, writes are only in the `--prune`/`--vacuum` paths.
- `OPENCODE_BIN` env var overrides the opencode executable (opencode.go:13).
- Source layout: opencode.go = data access, prune.go = artifact/orphan cleanup,
  model.go = state machine, view.go = rendering, styles.go = lipgloss theme, util.go = pure helpers.
- Tests never need a real opencode install: `model.deleter` and `model.collect` are injected and
  `opencodeBin` is pointed at a fake script.
- `maxConcurrent = 8` caps parallel deletes (model.go:41); opencode's store is single-writer SQLite.
- Headless test pattern: `tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(...))` + injected
  deleter, so no TTY needed.
