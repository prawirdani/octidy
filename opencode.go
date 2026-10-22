package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// opencodeBin is the opencode executable to invoke. Override with the
// OPENCODE_BIN env var (custom install path, or a fake binary in tests).
var opencodeBin = envOr("OPENCODE_BIN", "opencode")

// apiLimit is the page size requested from the v2 API. One huge page beats a
// cursor loop: the store returns everything in well under a second.
// shortcut: stores with >100k sessions truncate, page on cursor.next then.
const apiLimit = "100000"

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Session is one row of the session list. Both the v2 API and the v1 CLI JSON
// are mapped into this shape so the rest of the app is source-agnostic.
type Session struct {
	ID        string
	Title     string
	Updated   int64 // epoch milliseconds
	Directory string
}

// apiSession is the v2 `opencode api session.list` row. Its field names differ
// from the v1 CLI JSON, hence the separate struct + mapping.
type apiSession struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  struct {
		Updated int64 `json:"updated"`
	} `json:"time"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

type apiSessionList struct {
	Data []apiSession `json:"data"`
}

// loadSessions lists root sessions across ALL projects.
//
// opencode v2 scopes `opencode session list` to the current project, so it
// returned only that project's few sessions; the v2 API lists globally (and
// parentID=null keeps subagent child sessions out — deleting a root deletes
// them too). v1 has no `api` subcommand, so fall back to the old CLI there.
func loadSessions() ([]Session, error) {
	cmd := exec.Command(opencodeBin, "api", "session.list",
		"--param", "limit="+apiLimit, "--param", "parentID=null")
	if out, err := cmd.Output(); err == nil {
		var resp apiSessionList
		if err := json.Unmarshal(out, &resp); err == nil {
			sessions := make([]Session, 0, len(resp.Data))
			for _, s := range resp.Data {
				sessions = append(sessions, Session{
					ID:        s.ID,
					Title:     s.Title,
					Updated:   s.Time.Updated,
					Directory: s.Location.Directory,
				})
			}
			return sessions, nil
		}
	}
	return loadSessionsCLI()
}

// loadSessionsCLI is the opencode v1 path: project-scoped, no global listing.
func loadSessionsCLI() ([]Session, error) {
	cmd := exec.Command(opencodeBin, "session", "list", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("opencode session list failed: %s", ee.Stderr)
		}
		return nil, fmt.Errorf("running opencode: %w", err)
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		// opencode prints nothing (exit 0) when there are no sessions for
		// the current directory; treat that as an empty list, not an error.
		return nil, nil
	}
	var raw []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Updated   int64  `json:"updated"`
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing session list: %w", err)
	}
	sessions := make([]Session, 0, len(raw))
	for _, s := range raw {
		sessions = append(sessions, Session{
			ID:        s.ID,
			Title:     s.Title,
			Updated:   s.Updated,
			Directory: s.Directory,
		})
	}
	return sessions, nil
}

// deleteSession removes a single session by id (v2 deletes its children too).
// Arg slice (no shell) => no injection/quoting concerns even though ids are
// fixed ses_ tokens.
func deleteSession(id string) error {
	cmd := exec.Command(opencodeBin, "session", "delete", id)
	if out, err := cmd.CombinedOutput(); err != nil {
		if len(out) > 0 {
			return fmt.Errorf("%s", out)
		}
		return err
	}
	return nil
}
