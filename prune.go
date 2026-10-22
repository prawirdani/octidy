package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// danglingChildGrace protects a child session that is being created right now:
// for a brief moment its parent row may not be visible yet. Only strictly
// older dangling children are deleted.
const danglingChildGrace = time.Hour

// projectIDPattern matches opencode project ids (40-char sha1).
var projectIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func isProjectID(name string) bool { return projectIDPattern.MatchString(name) }

// serviceRunning reports whether the background opencode server holds the
// store open. VACUUM needs exclusive access, so maintenance refuses to start
// while it runs rather than fighting it for the lock.
func serviceRunning() bool {
	out, err := exec.Command(opencodeBin, "service", "status").Output()
	return err == nil && strings.Contains(string(out), "http")
}

// artifactRef matches the on-disk artifacts a session's stored parts point at:
// truncated tool output and captured shell output. Both live under the
// opencode data dir and are keyed by tool/shell call id, not by session id, so
// deleting the session leaves them behind unless we remove them ourselves.
var artifactRef = regexp.MustCompile(`(?:tool-output/tool_[A-Za-z0-9]+|shell/[0-9a-f]+/sh_[A-Za-z0-9]+\.out)`)

// dataDir resolves opencode's data directory (home of opencode.db, tool-output,
// shell, storage, snapshot). `opencode debug paths` is authoritative; the XDG
// fallback keeps this working if that command ever changes.
func dataDir() string {
	if out, err := exec.Command(opencodeBin, "debug", "paths").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "data" {
				return f[1]
			}
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

// openStore opens opencode's SQLite store. readOnly connections are safe while
// the background server runs; write connections are only used by maintenance
// commands, which require the server to be stopped.
func openStore(path string, readOnly bool) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)"
	if readOnly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// collectArtifacts returns the files and directories owned by each given
// session, keyed by session id so a failed delete can keep its artifacts. It
// must run BEFORE the deletes: it reads the stored parts that reference those
// paths, and opencode cascades them away.
func collectArtifacts(ids []string) map[string][]string {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string][]string, len(ids))
	dir := dataDir()
	if dir == "" {
		return out
	}
	db, err := openStore(filepath.Join(dir, "opencode.db"), true)
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()

	for _, id := range ids {
		seen := make(map[string]bool)
		add := func(p string) {
			p = filepath.Join(dir, filepath.Clean("/"+p))
			if !strings.HasPrefix(p, dir+string(os.PathSeparator)) || seen[p] {
				return
			}
			if _, err := os.Stat(p); err != nil {
				return
			}
			seen[p] = true
			out[id] = append(out[id], p)
		}
		// Legacy v1 layout: whole-session message dir + per-message part dirs.
		add(filepath.Join("storage", "message", id))
		rows, err := db.Query(`select id from message where session_id = ?`, id)
		if err == nil {
			for rows.Next() {
				var mid string
				if rows.Scan(&mid) == nil {
					add(filepath.Join("storage", "part", mid))
				}
			}
			_ = rows.Close()
		}
		// Stored parts point at tool-output/shell files by relative path.
		for _, table := range []string{"part", "session_message"} {
			rows, err := db.Query(`select data from `+table+` where session_id = ?`, id)
			if err != nil {
				continue
			}
			for rows.Next() {
				var data string
				if rows.Scan(&data) != nil {
					continue
				}
				for _, ref := range artifactRef.FindAllString(data, -1) {
					add(ref)
				}
			}
			_ = rows.Close()
		}
	}
	return out
}

// prunePaths deletes the collected artifacts and reports how many were removed
// and how many bytes they occupied.
func prunePaths(paths []string) (files int, bytes int64) {
	for _, p := range paths {
		var size int64
		_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				size += info.Size()
			}
			return nil
		})
		if err := os.RemoveAll(p); err != nil {
			continue
		}
		files++
		bytes += size
	}
	return files, bytes
}

// sweepStats is the outcome of a maintenance sweep.
type sweepStats struct {
	Files     int   // artifacts removed (files and directories)
	Bytes     int64 // bytes freed on disk by those artifacts
	Rows      int64 // orphan database rows deleted
	Reclaimed int64 // bytes returned to the filesystem by VACUUM
}

// sweepOrphans removes leftovers that no live session or project owns:
// dangling child sessions, unreachable messages/parts, and per-project or
// per-session directories under storage/, shell/ and snapshot/. vacuum also
// rewrites the database to hand free pages back to the filesystem, which
// requires the opencode service to be stopped first.
func sweepOrphans(dataDir string, vacuum bool) (sweepStats, error) {
	var st sweepStats
	dbPath := filepath.Join(dataDir, "opencode.db")
	if vacuum && serviceRunning() {
		return st, fmt.Errorf("the opencode service is running; VACUUM needs exclusive access (opencode service stop, then retry)")
	}
	db, err := openStore(dbPath, false)
	if err != nil {
		return st, fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	// Only rows that no live session can reach are deleted. Every NOT IN is
	// against the union of the v1 (`session`) and v2 (`session_v2`) tables:
	// a session that exists in only one of them is still live and must keep
	// its messages. Parts additionally keep their row when the owning session
	// is live, even if the message row is gone.
	childGrace := time.Now().Add(-danglingChildGrace).UnixMilli()
	orphanChildren := []string{
		`delete from session_v2 where parent_id is not null
		   and time_created < ? and parent_id not in (select id from session_v2)`,
		`delete from session where parent_id is not null
		   and time_created < ? and parent_id not in (select id from session)`,
	}
	stmts := []string{
		`delete from session_message where session_id not in (select id from session_v2)`,
		`delete from part where message_id not in (select id from message)
		   and session_id not in (select id from session)
		   and session_id not in (select id from session_v2)`,
		`delete from message where session_id not in (select id from session)
		   and session_id not in (select id from session_v2)`,
		`delete from todo where session_id not in (select id from session)
		   and session_id not in (select id from session_v2)`,
		`delete from session_share where session_id not in (select id from session)
		   and session_id not in (select id from session_v2)`,
	}
	tx, err := db.Begin()
	if err != nil {
		return st, err
	}
	// Children first, so their message/part rows are caught by the sweep below
	// instead of surviving until the next run.
	for _, q := range orphanChildren {
		for range 16 { // depth guard; a missing parent cannot loop forever
			res, execErr := tx.Exec(q, childGrace)
			if execErr != nil {
				_ = tx.Rollback()
				return st, execErr
			}
			n, _ := res.RowsAffected()
			st.Rows += n
			if n == 0 {
				break
			}
		}
	}
	for _, q := range stmts {
		res, execErr := tx.Exec(q)
		if execErr != nil {
			_ = tx.Rollback()
			return st, execErr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			st.Rows += n
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}

	sessionIDs := idSet(db, `select id from session`)
	for id := range idSet(db, `select id from session_v2`) {
		sessionIDs[id] = true
	}
	messageIDs := idSet(db, `select id from message`)
	for id := range idSet(db, `select id from session_message`) {
		messageIDs[id] = true
	}
	projectIDs := idSet(db, `select id from project`)
	for id := range idSet(db, `select distinct project_id from session_v2`) {
		projectIDs[id] = true
	}
	for id := range idSet(db, `select distinct project_id from worktree`) {
		projectIDs[id] = true
	}

	// Per-project directories are named after the project id (a 40-char sha1).
	// Anything not matching that shape is not ours to delete.
	for _, sub := range []string{"snapshot", "shell"} {
		for _, name := range dirsUnder(filepath.Join(dataDir, sub)) {
			if !isProjectID(name) || projectIDs[name] {
				continue
			}
			f, b := prunePaths([]string{filepath.Join(dataDir, sub, name)})
			st.Files, st.Bytes = st.Files+f, st.Bytes+b
		}
	}
	// Legacy v1 trees are keyed by session and message id.
	for _, name := range dirsUnder(filepath.Join(dataDir, "storage", "message")) {
		if sessionIDs[name] {
			continue
		}
		f, b := prunePaths([]string{filepath.Join(dataDir, "storage", "message", name)})
		st.Files, st.Bytes = st.Files+f, st.Bytes+b
	}
	for _, name := range dirsUnder(filepath.Join(dataDir, "storage", "part")) {
		if messageIDs[name] {
			continue
		}
		f, b := prunePaths([]string{filepath.Join(dataDir, "storage", "part", name)})
		st.Files, st.Bytes = st.Files+f, st.Bytes+b
	}

	if !vacuum {
		return st, nil
	}
	before := fileSize(dbPath)
	if _, err := db.Exec(`pragma wal_checkpoint(truncate)`); err != nil {
		return st, fmt.Errorf("checkpoint: %w", err)
	}
	if _, err := db.Exec(`vacuum`); err != nil {
		return st, fmt.Errorf("vacuum (stop the opencode service first: opencode service stop): %w", err)
	}
	// VACUUM writes the rebuilt pages through the WAL; truncate it again or the
	// freed bytes just move from the db file into the wal file.
	if _, err := db.Exec(`pragma wal_checkpoint(truncate)`); err != nil {
		return st, fmt.Errorf("post-vacuum checkpoint: %w", err)
	}
	st.Reclaimed = before - fileSize(dbPath)
	if st.Reclaimed < 0 {
		st.Reclaimed = 0
	}
	return st, nil
}

func dirsUnder(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func idSet(db *sql.DB, query string) map[string]bool {
	set := make(map[string]bool)
	rows, err := db.Query(query)
	if err != nil {
		return set
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			set[id] = true
		}
	}
	return set
}

func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}
