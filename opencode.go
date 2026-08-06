package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// opencodeBin is the opencode executable to invoke. Override with the
// OPENCODE_BIN env var (custom install path, or a fake binary in tests).
var opencodeBin = envOr("OPENCODE_BIN", "opencode")

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Session is one row from `opencode session list --format json`.
// Only the fields we render are decoded; extras in the JSON are ignored.
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Updated   int64  `json:"updated"` // epoch milliseconds
	Directory string `json:"directory"`
}

// loadSessions shells out to opencode and parses the JSON list.
func loadSessions() ([]Session, error) {
	cmd := exec.Command(opencodeBin, "session", "list", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("opencode session list failed: %s", ee.Stderr)
		}
		return nil, fmt.Errorf("running opencode: %w", err)
	}
	var sessions []Session
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("parsing session list: %w", err)
	}
	return sessions, nil
}

// deleteSession removes a single session by id. Arg slice (no shell) => no
// injection/quoting concerns even though ids are fixed ses_ tokens.
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
