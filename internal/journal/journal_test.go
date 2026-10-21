package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenAppendsConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "journal.jsonl")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := j.Record(Entry{Tool: "t", Status: "ok", BuildFixes: i}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	j.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); n++ {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		if e.Time.IsZero() {
			t.Fatalf("line %d: time not set", n)
		}
	}
	if n != 50 {
		t.Fatalf("got %d lines, want 50", n)
	}
}
