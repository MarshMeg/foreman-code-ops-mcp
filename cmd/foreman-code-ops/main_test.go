package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"":           {},
		"7d":         now.AddDate(0, 0, -7),
		"12h":        now.Add(-12 * time.Hour),
		"2026-10-01": time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	} {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"week", "-3x", "d"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q): expected an error", bad)
		}
	}
}

func TestStatsWithoutGit(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".foreman", "cache")
	os.MkdirAll(cache, 0o755)
	os.WriteFile(filepath.Join(cache, "journal.jsonl"), []byte(
		`{"time":"2026-10-01T10:00:00Z","tool":"upsert_symbol","status":"applied","written_by":"local","lang":"go","mode":"local"}`+"\n"), 0o644)
	var out, errOut bytes.Buffer
	if err := printStats([]string{"--project", dir}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "upsert_symbol: 1 calls, applied 1") || !strings.Contains(errOut.String(), "git history skipped") {
		t.Fatalf("stdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	out.Reset()
	if err := printStats([]string{"--project", dir, "--json"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"local": 1`) {
		t.Fatalf("json:\n%s", out.String())
	}
}
