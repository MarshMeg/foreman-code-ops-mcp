package style

import (
	"strings"
	"testing"
)

func TestSelect(t *testing.T) {
	md := "# Style\n\nGeneral rule.\n\n## Errors\nWrap errors.\n\n## Go\nUse %w.\n\n## TypeScript\nNo any.\n\n## Logging\nslog.\n"
	if got := Select(md, "go"); got != "# Style\n\nGeneral rule.\n\n## Errors\nWrap errors.\n\n## Go\nUse %w.\n\n## Logging\nslog." {
		t.Errorf("go:\n%s", got)
	}
	if got := Select(md, "tsx"); got != "# Style\n\nGeneral rule.\n\n## Errors\nWrap errors.\n\n## TypeScript\nNo any.\n\n## Logging\nslog." {
		t.Errorf("tsx:\n%s", got)
	}
	if Select("", "go") != "" {
		t.Error("empty")
	}
}

func TestSetSection(t *testing.T) {
	md := "# Style\n\n## Errors\nWrap.\n\n## Go\nOld.\n\n## Logging\nslog.\n"
	got := SetSection(md, "go", "Use %w.\nNo panics.")
	if want := "# Style\n\n## Errors\nWrap.\n\n## Go\n\nUse %w.\nNo panics.\n\n## Logging\nslog.\n"; got != want {
		t.Errorf("replace middle:\n%q\nwant\n%q", got, want)
	}
	got = SetSection(md, "Logging", "## Logging\nzap.")
	if want := "# Style\n\n## Errors\nWrap.\n\n## Go\nOld.\n\n## Logging\nzap.\n"; got != want {
		t.Errorf("replace last:\n%q", got)
	}
	got = SetSection(md, "Testing", "Table tests.")
	if !strings.HasSuffix(got, "slog.\n\n## Testing\n\nTable tests.\n") {
		t.Errorf("append:\n%q", got)
	}
	if got := SetSection("", "Go", "x"); got != "## Go\n\nx\n" {
		t.Errorf("empty: %q", got)
	}
	if s := Sections(md); strings.Join(s, ",") != "Errors,Go,Logging" {
		t.Errorf("sections = %v", s)
	}
}
