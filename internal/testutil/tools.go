package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// RequireTools prepends the directories listed in FOREMAN_TEST_PATH to PATH and skips the
// test unless every named tool is found. CI installs the tools globally; locally they can
// live in a scratch prefix.
func RequireTools(t *testing.T, names ...string) {
	t.Helper()
	if extra := os.Getenv("FOREMAN_TEST_PATH"); extra != "" {
		t.Setenv("PATH", extra+string(filepath.ListSeparator)+os.Getenv("PATH"))
	}
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not installed (set FOREMAN_TEST_PATH)", n)
		}
	}
}
