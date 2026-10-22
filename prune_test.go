package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStore creates a minimal opencode.db with the tables the prune code
// queries, so tests never touch a real install.
func newTestStore(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := openStore(filepath.Join(dir, "opencode.db"), false)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{
		`create table session (id text primary key, parent_id text, time_created integer)`,
		`create table session_v2 (id text primary key, parent_id text, time_created integer)`,
		`create table message (id text primary key, session_id text)`,
		`create table part (id text primary key, message_id text, session_id text, data text)`,
		`create table session_message (id text primary key, session_id text, data text)`,
		`create table todo (session_id text, content text)`,
		`create table session_share (session_id text, id text)`,
		`create table project (id text primary key)`,
		`create table worktree (project_id text, directory text)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("ddl %q: %v", ddl, err)
		}
	}
	return db
}

// fakeOpencodeData points dataDir() at dir by faking `opencode debug paths`.
func fakeOpencodeData(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake not portable to windows")
	}
	bin := filepath.Join(t.TempDir(), "fakeoc")
	script := "#!/usr/bin/env bash\nprintf 'data       " + dir + "\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := opencodeBin
	opencodeBin = bin
	t.Cleanup(func() { opencodeBin = old })
}

// TestCollectArtifactsFindsSessionFiles verifies the pre-delete scan resolves
// tool-output/shell references and legacy storage dirs for a session, and that
// pruning removes them.
func TestCollectArtifactsFindsSessionFiles(t *testing.T) {
	dir := t.TempDir()
	db := newTestStore(t, dir)
	fakeOpencodeData(t, dir)

	for _, d := range []string{"tool-output", "shell/0dbf5b", "storage/message/ses_keep", "storage/part/msg_keep"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tool := filepath.Join(dir, "tool-output", "tool_abc123")
	shell := filepath.Join(dir, "shell", "0dbf5b", "sh_def456.out")
	for _, p := range []string{tool, shell, filepath.Join(dir, "storage/message/ses_keep/m1.json"), filepath.Join(dir, "storage/part/msg_keep/p1.json")} {
		if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`insert into session_v2 (id) values ('ses_keep')`)
	exec(`insert into message (id, session_id) values ('msg_keep', 'ses_keep')`)
	exec(`insert into part (id, message_id, session_id, data) values ('prt_1', 'msg_keep', 'ses_keep', ?)`,
		`{"type":"text","text":"saved to /home/x/.local/share/opencode/tool-output/tool_abc123 and shell/0dbf5b/sh_def456.out"}`)

	got := collectArtifacts([]string{"ses_keep"})
	want := map[string]bool{tool: true, shell: true,
		filepath.Join(dir, "storage/message/ses_keep"): true,
		filepath.Join(dir, "storage/part/msg_keep"):    true}
	if len(got) != 1 || len(got["ses_keep"]) != len(want) {
		t.Fatalf("collectArtifacts=%v want %d paths under ses_keep", got, len(want))
	}
	for _, p := range got["ses_keep"] {
		if !want[p] {
			t.Fatalf("unexpected path %q in %v", p, got)
		}
	}

	// Only successfully deleted sessions get their files pruned.
	results := []result{{id: "ses_keep", err: errors.New("boom")}}
	if paths := deletedArtifacts(results, got); len(paths) != 0 {
		t.Fatalf("failed delete still pruned %v", paths)
	}
	results = []result{{id: "ses_keep"}}

	files, bytes := prunePaths(deletedArtifacts(results, got))
	if files != len(want) || bytes == 0 {
		t.Fatalf("prunePaths=%d files/%d bytes want %d files", files, bytes, len(want))
	}
	for p := range want {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%q still exists after prune", p)
		}
	}
}

// TestSweepOrphansRemovesUnreachableData verifies dangling child sessions,
// orphan messages and project/session directories are removed while live ones
// survive.
func TestSweepOrphansRemovesUnreachableData(t *testing.T) {
	dir := t.TempDir()
	db := newTestStore(t, dir)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	liveProj := strings.Repeat("ab", 20) // 40-char sha1-shaped project id
	goneProj := strings.Repeat("cd", 20)
	exec(`insert into project (id) values (?)`, liveProj)
	exec(`insert into session_v2 (id) values ('ses_live')`)
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	exec(`insert into session_v2 (id, parent_id, time_created) values ('ses_orphan', 'ses_gone', ?)`, old)
	// A grandchild of the dangling child: caught only by repeating the sweep.
	exec(`insert into session_v2 (id, parent_id, time_created) values ('ses_grandchild', 'ses_orphan', ?)`, old)
	// A child created moments ago may simply not have its parent committed yet.
	exec(`insert into session_v2 (id, parent_id, time_created) values ('ses_fresh', 'ses_also_gone', ?)`,
		time.Now().UnixMilli())
	exec(`insert into message (id, session_id) values ('msg_live', 'ses_live')`)
	exec(`insert into message (id, session_id) values ('msg_orphan', 'ses_gone')`)
	exec(`insert into session_message (id, session_id) values ('sm_orphan', 'ses_gone')`)
	// A part whose message row is gone but whose session is live must survive.
	exec(`insert into part (id, message_id, session_id) values ('prt_live', 'msg_gone', 'ses_live')`)
	exec(`insert into part (id, message_id, session_id) values ('prt_orphan', 'msg_gone', 'ses_gone')`)

	for _, d := range []string{
		"snapshot/" + liveProj, "snapshot/" + goneProj,
		"shell/" + liveProj, "shell/" + goneProj,
		"storage/message/ses_live", "storage/message/ses_gone",
		"storage/part/msg_live", "storage/part/msg_orphan",
	} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	st, err := sweepOrphans(dir, false)
	if err != nil {
		t.Fatalf("sweepOrphans: %v", err)
	}
	if st.Rows != 5 {
		t.Errorf("rows=%d want 5 (2 dangling children, 1 message, 1 session_message, 1 part)", st.Rows)
	}
	var n int
	if err := db.QueryRow(`select count(*) from session_v2 where id = 'ses_fresh'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("recently created child was deleted (n=%d err=%v)", n, err)
	}
	if err := db.QueryRow(`select count(*) from part where id = 'prt_live'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("part of a live session was deleted (n=%d err=%v)", n, err)
	}
	for _, gone := range []string{
		"snapshot/" + goneProj, "shell/" + goneProj,
		"storage/message/ses_gone", "storage/part/msg_orphan",
	} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%q survived prune", gone)
		}
	}
	for _, kept := range []string{
		"snapshot/" + liveProj, "shell/" + liveProj,
		"storage/message/ses_live", "storage/part/msg_live",
	} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%q was pruned: %v", kept, err)
		}
	}
}

// TestSweepOrphansRefusesVacuumWhileServiceRuns verifies VACUUM does not even
// open the store when the background server is up: it would fight a live
// writer for exclusive access.
func TestSweepOrphansRefusesVacuumWhileServiceRuns(t *testing.T) {
	dir := t.TempDir()
	newTestStore(t, dir)
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake not portable to windows")
	}
	bin := filepath.Join(t.TempDir(), "fakeoc")
	script := "#!/usr/bin/env bash\nprintf 'http://127.0.0.1:49374\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := opencodeBin
	opencodeBin = bin
	t.Cleanup(func() { opencodeBin = old })

	if _, err := sweepOrphans(dir, true); err == nil {
		t.Fatal("sweepOrphans(vacuum) ran while the service was up")
	}
}
