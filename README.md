# octidy

Interactive terminal UI (TUI) to browse and **bulk-delete opencode sessions** —
clean up stale opencode chat sessions from your terminal in seconds. A thin
wrapper over the `opencode` CLI, so no opencode storage internals are touched.

[![Go Reference](https://pkg.go.dev/badge/github.com/prawirdani/octidy.svg)](https://pkg.go.dev/github.com/prawirdani/octidy)
[![codecov](https://codecov.io/github/Prawirdani/octidy/graph/badge.svg)](https://codecov.io/github/Prawirdani/octidy)
[![Go Report Card](https://goreportcard.com/badge/github.com/prawirdani/octidy)](https://goreportcard.com/report/github.com/prawirdani/octidy)
![Build Status](https://github.com/prawirdani/octidy/actions/workflows/ci.yml/badge.svg)

![octidy demo](assets/demo.gif)

## Features

- List root sessions from every project (title, relative age, working directory)
- Multi-select with `space`, select-all (`a`) / select-none (`n`)
- Live `/` filter over title and working directory
- `y/N` confirmation gate before anything is deleted
- Concurrent deletes (capped at 8 in flight) with live ✓/✗ progress
- Failure summary + non-zero exit code if any delete fails
- Prunes the deleted sessions' leftover files; `--prune` / `--vacuum` sweep
  orphans and compact the database
- Works with opencode v2 (global listing via `opencode api`) and falls back to
  the v1 CLI on older installs

## Requirements

- Go 1.26+ to build from source
- The `opencode` CLI on `$PATH` at runtime (see `OPENCODE_BIN` below)
- A TTY for the interactive UI

## Installation

Prebuilt binaries for every OS/arch are attached to each
[release](https://github.com/prawirdani/octidy/releases) (built by CI on every
`v*` tag). Grab them with the one-liner below, download manually, or build from
source.

### One-liner

**Linux / macOS** (installs to `/usr/local/bin`):

```sh
curl -fsSL "https://github.com/prawirdani/octidy/releases/latest/download/octidy-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" -o octidy && chmod +x octidy && sudo mv octidy /usr/local/bin/octidy
```

**Windows** (PowerShell — run in a directory on your `PATH`, e.g. `C:\Users\<you>\bin`):

```powershell
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }; Invoke-WebRequest "https://github.com/prawirdani/octidy/releases/latest/download/octidy-windows-$arch.exe" -OutFile octidy.exe
```

### Asset matrix

Prefer manual download? Pick the file matching your OS and CPU:

| OS      | CPU architecture | Asset                     |
|---------|------------------|---------------------------|
| Linux   | x86_64 (amd64)   | `octidy-linux-amd64`      |
| Linux   | aarch64 (arm64)  | `octidy-linux-arm64`      |
| macOS   | x86_64 (amd64)   | `octidy-darwin-amd64`     |
| macOS   | Apple Silicon    | `octidy-darwin-arm64`     |
| Windows | x86_64 (amd64)   | `octidy-windows-amd64.exe`|
| Windows | arm64            | `octidy-windows-arm64.exe`|

Find your CPU with `uname -m` on Linux/macOS (`x86_64` → amd64,
`aarch64` → arm64), or `echo $env:PROCESSOR_ARCHITECTURE` in PowerShell.

Every asset is reachable at the stable URL
`https://github.com/prawirdani/octidy/releases/latest/download/<asset>`, so you
can pin a specific version by replacing `latest` with a tag (e.g. `v0.1.0`).

### Linux

```sh
chmod +x octidy-linux-amd64
sudo mv octidy-linux-amd64 /usr/local/bin/octidy
```

The binary is statically linked (CGO disabled) and runs on both glibc- and
musl-based distros.

### macOS

```sh
chmod +x octidy-darwin-arm64   # or octidy-darwin-amd64 on Intel Macs
sudo mv octidy-darwin-arm64 /usr/local/bin/octidy
```

If Gatekeeper refuses to run the downloaded binary, clear the quarantine flag:

```sh
xattr -dr com.apple.quarantine /usr/local/bin/octidy
```

### Windows

```powershell
# from PowerShell, into a directory on your PATH (or just where you'll run it)
Move-Item .\octidy-windows-amd64.exe C:\Users\<you>\bin\octidy.exe
```

Run `octidy.exe` from any terminal afterwards. Use the arm64 asset on ARM64
Windows.

### Verify

```sh
octidy
```

The TUI should open immediately. It needs the `opencode` CLI on `$PATH` (see
`OPENCODE_BIN` below) and a real terminal — piping or backgrounding the process
won't work.

### Build from source

Requires Go 1.26+:

```sh
go install .   # or: make build
```

## Usage

```sh
octidy
```

Keybindings:

| Key            | Action                                   |
|----------------|------------------------------------------|
| `↑` / `↓` / `k` / `j` | Move cursor                       |
| `g` / `home`   | Jump to first session                    |
| `G` / `end`    | Jump to last session                     |
| `space`        | Toggle selection                          |
| `a`            | Select all                                |
| `n`            | Clear selection                           |
| `enter`        | Proceed to confirmation                   |
| `y`            | Confirm deletion (irreversible)           |
| `N` / `esc` / `q` | Go back / quit                         |

Exit code is non-zero when one or more deletes fail.

### Pruning leftovers

`opencode session delete` removes the session **rows** (messages, parts, child
sessions) but leaves the bytes behind: SQLite never shrinks its file, and
truncated tool output, captured shell output and legacy storage directories
survive the delete.

octidy cleans up after itself:

- after every delete it removes the artifacts the deleted sessions owned
  (tool-output files, `shell/<project>/sh_*.out`, legacy `storage/` dirs);
- `octidy --prune` sweeps orphans: dangling child sessions, unreachable
  messages/parts, and `snapshot/`, `shell/` or `storage/` directories whose
  project/session no longer exists;
- `octidy --vacuum` does the sweep, then checkpoints the WAL and `VACUUM`s
  `opencode.db` to hand free pages back to the filesystem.

```sh
octidy --prune     # safe with opencode running
octidy --vacuum    # rewrites the whole db; stop the service first
opencode service stop && octidy --vacuum
```

VACUUM needs free disk roughly the size of the written database and takes
seconds per GB. `--vacuum` refuses to start while the opencode service is
running, because it needs exclusive access to the store.

### Environment

| Variable       | Description                                             |
|----------------|---------------------------------------------------------|
| `OPENCODE_BIN` | Path to the `opencode` executable (default: `opencode`) |

## How it works

1. Loads root sessions across **all** projects via
   `opencode api session.list --param limit=100000 --param parentID=null`
   (opencode v2 scopes `opencode session list` to the current project and caps it
   at `-n 100`, which is why it showed only a few sessions). opencode v1 installs,
   which have no `api` subcommand, fall back to
   `opencode session list --format json` (project-scoped by design).
2. Deletes chosen sessions via `opencode session delete <id>` — in v2 this also
   deletes the session's child (subagent) sessions.
3. Before the delete it records the artifacts those sessions own (from the
   stored parts that reference them), and removes them once the TUI exits —
   opencode itself does not.

Deletes run in a pool of at most 8 concurrent subprocesses (`maxConcurrent` in
`model.go`), because opencode's store is a single-writer SQLite database.

## Development

```sh
make build        # build for the current OS
make build-all    # build for every OS/arch into dist/
make test         # run tests
make test-race    # run tests with the race detector
make lint         # golangci-lint, falls back to go vet
make clean        # remove dist/
```

## Layout

| File           | Responsibility                        |
|----------------|---------------------------------------|
| `opencode.go`  | Data access: load/delete sessions     |
| `model.go`     | State machine (Bubble Tea model)      |
| `view.go`      | Rendering                             |
| `styles.go`    | lipgloss theme                        |
| `util.go`      | Pure helpers                          |
| `prune.go`     | Artifact collection, orphan sweep, VACUUM |

Tests never need a real opencode install: `model.deleter` and `model.collect`
are injected and `opencodeBin` points at a fake script. `prune_test.go` builds
a throwaway SQLite store with `modernc.org/sqlite` (pure Go, no cgo).

## License

MIT
