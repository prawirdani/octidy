package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoadSessionsWithFakeBin points opencodeBin at a fake executable and
// verifies loadSessions runs it and parses the JSON. Also exercises the
// OPENCODE_BIN override path.
func TestLoadSessionsWithFakeBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake not portable to windows")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakeoc")
	script := `#!/usr/bin/env bash
if [ "$1 $2" = "session list" ]; then
  echo '[{"id":"ses_x","title":"Fake","updated":123,"directory":"/tmp"}]'
fi
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	old := opencodeBin
	opencodeBin = fake
	defer func() { opencodeBin = old }()

	got, err := loadSessions()
	if err != nil {
		t.Fatalf("loadSessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ses_x" || got[0].Title != "Fake" {
		t.Fatalf("loadSessions=%+v want one ses_x/Fake", got)
	}
}

// TestLoadSessionsEmptyOutput verifies opencode printing nothing (exit 0) for
// "no sessions in this directory" is treated as an empty list, not a parse
// error.
func TestLoadSessionsEmptyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake not portable to windows")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakeoc")
	if err := os.WriteFile(fake, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := opencodeBin
	opencodeBin = fake
	defer func() { opencodeBin = old }()

	got, err := loadSessions()
	if err != nil {
		t.Fatalf("loadSessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("loadSessions=%+v want empty list", got)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("OC_TEST_KEY", "")
	if got := envOr("OC_TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("envOr empty=%q want fallback", got)
	}
	t.Setenv("OC_TEST_KEY", "value")
	if got := envOr("OC_TEST_KEY", "fallback"); got != "value" {
		t.Errorf("envOr set=%q want value", got)
	}
}
