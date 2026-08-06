# octidy

Interactive TUI (Bubble Tea) for browsing and deleting opencode sessions.
A thin wrapper over the `opencode` CLI — no opencode storage internals are
touched.

[![Go Reference](https://pkg.go.dev/badge/github.com/prawirdani/octidy.svg)](https://pkg.go.dev/github.com/prawirdani/octidy)
[![codecov](https://codecov.io/github/Prawirdani/octidy/graph/badge.svg)](https://codecov.io/github/Prawirdani/octidy)
[![Go Report Card](https://goreportcard.com/badge/github.com/prawirdani/octidy)](https://goreportcard.com/report/github.com/prawirdani/octidy)
![Build Status](https://github.com/prawirdani/octidy/actions/workflows/ci.yml/badge.svg)

## Features

- List sessions with title, relative age, and working directory
- Multi-select with `space`, select-all (`a`) / select-none (`n`)
- `y/N` confirmation gate before anything is deleted
- Concurrent deletes (capped at 8 in flight) with live ✓/✗ progress
- Failure summary + non-zero exit code if any delete fails
- Works over the existing `opencode` CLI, so it stays compatible with whatever
  opencode version you have installed

## Requirements

- Go 1.26+ to build from source
- The `opencode` CLI on `$PATH` at runtime (see `OPENCODE_BIN` below)
- A TTY for the interactive UI

## Install

From source:

```sh
go install .            # or: make build
```

Or download a prebuilt binary from the Releases page of this repository.

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

### Environment

| Variable       | Description                                             |
|----------------|---------------------------------------------------------|
| `OPENCODE_BIN` | Path to the `opencode` executable (default: `opencode`) |

## How it works

1. Loads sessions via `opencode session list --format json`
2. Deletes chosen sessions via `opencode session delete <id>`

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

Tests never need a real opencode install: `model.deleter` is injected and
`opencodeBin` points at a fake script.

## License

MIT
