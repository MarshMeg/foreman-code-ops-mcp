package stats

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gitops"
)

const journalFixture = `{"time":"2026-10-01T10:00:00Z","tool":"upsert_symbol","status":"applied","written_by":"local","lang":"go","mode":"local","duration_ms":4000,"generator":{"model":"openai/qwen","prompt_tokens":1000,"completion_tokens":200}}
{"time":"2026-10-01T10:05:00Z","tool":"upsert_symbol","status":"applied","written_by":"local","build_fixes":2,"reviews":1,"lang":"go","mode":"local","duration_ms":9000,"generator":{"model":"openai/qwen","prompt_tokens":3000,"completion_tokens":600},"validator":{"model":"openrouter/glm","prompt_tokens":2000,"completion_tokens":100,"cached_tokens":1500,"cost":0.0004}}
{"time":"2026-10-02T09:00:00Z","tool":"upsert_symbol","status":"applied","written_by":"validator","build_fixes":3,"fallback":1,"lang":"python","mode":"local","duration_ms":20000}
{"time":"2026-10-02T09:10:00Z","tool":"upsert_symbol","status":"applied","written_by":"validator","lang":"python","mode":"direct","duration_ms":3000}
{"time":"2026-10-02T09:20:00Z","tool":"upsert_symbol","status":"failed","written_by":"none","build_fixes":3,"lang":"go","mode":"local","duration_ms":15000}
{"time":"2026-10-02T09:30:00Z","tool":"upsert_symbol","status":"error","error":"boom","duration_ms":5}
this line was cut by a crash
{"time":"2026-10-03T09:00:00Z","tool":"check","status":"ok","duration_ms":800}

`

func TestSummarizeJournal(t *testing.T) {
	rep, err := summarizeJournal(strings.NewReader(journalFixture), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 7 || rep.Tools["upsert_symbol"].Calls != 6 || rep.Tools["upsert_symbol"].Errors != 1 {
		t.Fatalf("entries %d, tools %+v", rep.Entries, rep.Tools["upsert_symbol"])
	}
	u := rep.Upserts
	if u.Total != 5 || u.Applied != 4 || u.Failed != 1 || u.Local != 2 || u.Validator != 2 || u.FirstTry != 1 ||
		u.Direct != 1 || u.DirectApplied != 1 || u.BuildFixes != 8 || u.Reviews != 1 || u.Fallback != 1 {
		t.Fatalf("upserts %+v", u)
	}
	// 2 of 3 applied local-mode results were written locally.
	if s := u.LocalShare(); s < 0.66 || s > 0.67 {
		t.Fatalf("local share %v", s)
	}
	if rep.ByLang["go"].Total != 3 || rep.ByLang["python"].Validator != 2 {
		t.Fatalf("by lang go %+v python %+v", rep.ByLang["go"], rep.ByLang["python"])
	}
	if m := rep.Models["openai/qwen"]; m.Calls != 2 || m.PromptTokens != 4000 || m.CompletionTokens != 800 {
		t.Fatalf("qwen %+v", m)
	}
	if m := rep.Models["openrouter/glm"]; m.CachedTokens != 1500 || m.Cost != 0.0004 {
		t.Fatalf("glm %+v", m)
	}
	if p := rep.Tools["upsert_symbol"]; p.P50MS != 4000 || p.P95MS != 20000 {
		t.Fatalf("percentiles %+v", p)
	}

	since, _ := time.Parse(time.RFC3339, "2026-10-02T09:15:00Z")
	rep, err = summarizeJournal(strings.NewReader(journalFixture), since)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 3 || rep.Upserts.Total != 1 || rep.Upserts.Failed != 1 {
		t.Fatalf("since: %+v", rep.Upserts)
	}
}

func TestSummarizeCommits(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	rep := SummarizeCommits([]gitops.CommitInfo{
		{Time: at("2026-10-03"), Trailers: map[string]string{gitops.TrailerWrittenBy: "local (qwen)", gitops.TrailerAttempts: "build=0 review=0", gitops.TrailerReviewedBy: "openrouter/glm"}},
		{Time: at("2026-10-01"), Trailers: map[string]string{gitops.TrailerWrittenBy: "local (qwen)", gitops.TrailerAttempts: "build=2 review=1"}},
		{Time: at("2026-10-02"), Trailers: map[string]string{gitops.TrailerWrittenBy: "validator (openrouter/glm)", gitops.TrailerAttempts: "build=3 review=0 fallback=2"}},
	})
	if rep.Commits != 3 || rep.Local != 2 || rep.Validator != 1 || rep.FirstTry != 1 || rep.Reviewed != 1 ||
		rep.BuildFixes != 5 || rep.Reviews != 1 || rep.Fallback != 2 || rep.WrittenBy["local (qwen)"] != 2 ||
		!rep.First.Equal(at("2026-10-01")) || !rep.Last.Equal(at("2026-10-03")) {
		t.Fatalf("got %+v", rep)
	}
}

func TestWriteText(t *testing.T) {
	j, _ := summarizeJournal(strings.NewReader(journalFixture), time.Time{})
	var b bytes.Buffer
	WriteText(&b, Report{Journal: j, Git: SummarizeCommits(nil)})
	out := b.String()
	for _, want := range []string{
		"Journal: 7 tool calls, 2026-10-01 – 2026-10-03",
		"upsert_symbol: 5 calls, applied 4, failed 1, refused 0; local 2 (67% of local-mode, first try 1), validator 2 (direct 1)",
		"  go: 3 calls",
		"openrouter/glm", "75%", "$0.0004",
		"Git: no foreman commits",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
