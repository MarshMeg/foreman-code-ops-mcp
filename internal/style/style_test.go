package style

import "testing"

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
