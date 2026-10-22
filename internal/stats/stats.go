// Package stats summarizes foreman's work from two sources: the journal of tool calls
// (.foreman/cache/journal.jsonl, local to one machine) and the Foreman-* trailers of git
// commits (shared with everyone who has the history).
package stats

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/gitops"
	"github.com/MarshMeg/foreman-code-ops-mcp/internal/journal"
)

// Report is the summary printed by `foreman-code-ops stats`.
type Report struct {
	Since   time.Time      `json:"since,omitzero"`
	Journal *JournalReport `json:"journal,omitempty"`
	Git     *GitReport     `json:"git,omitempty"`
}

// JournalReport summarizes journal entries.
type JournalReport struct {
	Entries int                     `json:"entries"`
	First   time.Time               `json:"first,omitzero"`
	Last    time.Time               `json:"last,omitzero"`
	Tools   map[string]*ToolStats   `json:"tools"`
	Upserts Outcomes                `json:"upserts"`
	ByLang  map[string]*Outcomes    `json:"by_lang,omitempty"`
	Models  map[string]*ModelTokens `json:"models,omitempty"`
}

// ToolStats is the call count and latency of one tool.
type ToolStats struct {
	Calls  int   `json:"calls"`
	Errors int   `json:"errors"`
	P50MS  int64 `json:"p50_ms"`
	P95MS  int64 `json:"p95_ms"`
	ms     []int64
}

// Outcomes counts upsert_symbol results.
type Outcomes struct {
	Total   int `json:"total"`
	Applied int `json:"applied"`
	Failed  int `json:"failed"`
	Refused int `json:"refused"`
	Direct  int `json:"direct"`
	// DirectApplied are applied direct-mode results (always written by the validator).
	DirectApplied int `json:"direct_applied"`
	// Local and Validator split the applied results by who wrote the code.
	Local     int `json:"local"`
	Validator int `json:"validator"`
	// FirstTry is local code that passed without fix or review rounds.
	FirstTry   int `json:"first_try"`
	BuildFixes int `json:"build_fixes"`
	Reviews    int `json:"reviews"`
	Fallback   int `json:"fallback"`
}

// LocalShare is the main quality metric (plan §16): the share of applied local-mode
// results written by the local model, without a fallback to the validator.
func (o Outcomes) LocalShare() float64 {
	if n := o.Applied - o.DirectApplied; n > 0 {
		return float64(o.Local) / float64(n)
	}
	return 0
}

func (o *Outcomes) add(e journal.Entry) {
	o.Total++
	if e.Mode == "direct" {
		o.Direct++
	}
	switch e.Status {
	case "applied":
		o.Applied++
		if e.Mode == "direct" {
			o.DirectApplied++
		}
		switch e.WrittenBy {
		case "local":
			o.Local++
			if e.BuildFixes == 0 && e.Reviews == 0 {
				o.FirstTry++
			}
		case "validator":
			o.Validator++
		}
	case "failed":
		o.Failed++
	case "refused":
		o.Refused++
	}
	o.BuildFixes += e.BuildFixes
	o.Reviews += e.Reviews
	o.Fallback += e.Fallback
}

// ModelTokens accumulates token usage and cost of one model.
type ModelTokens struct {
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	Cost             float64 `json:"cost"`
}

// GitReport summarizes foreman commits.
type GitReport struct {
	Commits int `json:"commits"`
	// WrittenBy counts commits by trailer value, e.g. "local (qwen)" or "validator (openrouter/glm)".
	WrittenBy  map[string]int `json:"written_by"`
	Local      int            `json:"local"`
	Validator  int            `json:"validator"`
	Reviewed   int            `json:"reviewed"`
	FirstTry   int            `json:"first_try"`
	BuildFixes int            `json:"build_fixes"`
	Reviews    int            `json:"reviews"`
	Fallback   int            `json:"fallback"`
	First      time.Time      `json:"first,omitzero"`
	Last       time.Time      `json:"last,omitzero"`
}

// ReadJournal summarizes the journal at path; a missing file returns (nil, nil).
func ReadJournal(path string, since time.Time) (*JournalReport, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return summarizeJournal(f, since)
}

func summarizeJournal(r io.Reader, since time.Time) (*JournalReport, error) {
	rep := &JournalReport{Tools: map[string]*ToolStats{}, ByLang: map[string]*Outcomes{}, Models: map[string]*ModelTokens{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		var e journal.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			// A line cut by a crash must not hide the rest of the journal.
			continue
		}
		if !since.IsZero() && e.Time.Before(since) {
			continue
		}
		rep.Entries++
		if rep.First.IsZero() || e.Time.Before(rep.First) {
			rep.First = e.Time
		}
		if e.Time.After(rep.Last) {
			rep.Last = e.Time
		}
		ts := rep.Tools[e.Tool]
		if ts == nil {
			ts = &ToolStats{}
			rep.Tools[e.Tool] = ts
		}
		ts.Calls++
		if e.Status == "error" {
			ts.Errors++
		}
		ts.ms = append(ts.ms, e.DurationMS)
		if e.Tool == "upsert_symbol" && e.Status != "error" {
			rep.Upserts.add(e)
			if e.Lang != "" {
				if rep.ByLang[e.Lang] == nil {
					rep.ByLang[e.Lang] = &Outcomes{}
				}
				rep.ByLang[e.Lang].add(e)
			}
		}
		for _, u := range []*journal.Usage{e.Generator, e.Validator} {
			if u == nil {
				continue
			}
			name := u.Model
			if name == "" {
				name = "unknown"
			}
			m := rep.Models[name]
			if m == nil {
				m = &ModelTokens{}
				rep.Models[name] = m
			}
			m.Calls++
			m.PromptTokens += u.PromptTokens
			m.CompletionTokens += u.CompletionTokens
			m.CachedTokens += u.CachedTokens
			m.Cost += u.Cost
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, ts := range rep.Tools {
		slices.Sort(ts.ms)
		ts.P50MS, ts.P95MS = percentile(ts.ms, 50), percentile(ts.ms, 95)
	}
	return rep, nil
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted)*p + 99) / 100
	return sorted[max(i-1, 0)]
}

// SummarizeCommits turns foreman commits into a GitReport.
func SummarizeCommits(commits []gitops.CommitInfo) *GitReport {
	rep := &GitReport{WrittenBy: map[string]int{}}
	for _, c := range commits {
		rep.Commits++
		if rep.First.IsZero() || c.Time.Before(rep.First) {
			rep.First = c.Time
		}
		if c.Time.After(rep.Last) {
			rep.Last = c.Time
		}
		wb := c.Trailers[gitops.TrailerWrittenBy]
		rep.WrittenBy[wb]++
		a := ParseAttempts(c.Trailers[gitops.TrailerAttempts])
		switch {
		case strings.HasPrefix(wb, "local"):
			rep.Local++
			if a["build"] == 0 && a["review"] == 0 {
				rep.FirstTry++
			}
		case strings.HasPrefix(wb, "validator"):
			rep.Validator++
		}
		if c.Trailers[gitops.TrailerReviewedBy] != "" {
			rep.Reviewed++
		}
		rep.BuildFixes += a["build"]
		rep.Reviews += a["review"]
		rep.Fallback += a["fallback"]
	}
	return rep
}

// ParseAttempts reads a Foreman-Attempts value such as "build=1 review=0 fallback=2".
func ParseAttempts(v string) map[string]int {
	out := map[string]int{}
	for _, f := range strings.Fields(v) {
		if k, n, ok := strings.Cut(f, "="); ok {
			if i, err := strconv.Atoi(n); err == nil {
				out[k] = i
			}
		}
	}
	return out
}

// WriteText renders the report for a terminal.
func WriteText(w io.Writer, r Report) {
	if !r.Since.IsZero() {
		fmt.Fprintf(w, "Since %s\n\n", r.Since.Format("2006-01-02 15:04"))
	}
	if j := r.Journal; j != nil && j.Entries > 0 {
		fmt.Fprintf(w, "Journal: %d tool calls, %s – %s\n\n", j.Entries, j.First.Format("2006-01-02"), j.Last.Format("2006-01-02"))
		writeOutcomes(w, "upsert_symbol", j.Upserts)
		for _, l := range sortedKeys(j.ByLang) {
			writeOutcomes(w, "  "+l, *j.ByLang[l])
		}
		fmt.Fprintf(w, "\n%-16s %6s %6s %8s %8s\n", "tool", "calls", "errors", "p50", "p95")
		for _, name := range sortedKeys(j.Tools) {
			t := j.Tools[name]
			fmt.Fprintf(w, "%-16s %6d %6d %8s %8s\n", name, t.Calls, t.Errors, ms(t.P50MS), ms(t.P95MS))
		}
		if len(j.Models) > 0 {
			fmt.Fprintf(w, "\n%-40s %6s %10s %10s %8s %9s\n", "model", "calls", "prompt", "output", "cached", "cost")
			for _, name := range sortedKeys(j.Models) {
				m := j.Models[name]
				fmt.Fprintf(w, "%-40s %6d %10d %10d %7.0f%% %9s\n", name, m.Calls, m.PromptTokens, m.CompletionTokens,
					pct(m.CachedTokens, m.PromptTokens), cost(m.Cost))
			}
		}
	} else {
		fmt.Fprintln(w, "Journal: no entries")
	}
	fmt.Fprintln(w)
	if g := r.Git; g != nil && g.Commits > 0 {
		fmt.Fprintf(w, "Git: %d foreman commits, %s – %s\n", g.Commits, g.First.Format("2006-01-02"), g.Last.Format("2006-01-02"))
		fmt.Fprintf(w, "  written locally %d (%.0f%%), first try %d; by the validator %d; reviewed %d\n",
			g.Local, pct(g.Local, g.Local+g.Validator), g.FirstTry, g.Validator, g.Reviewed)
		fmt.Fprintf(w, "  rounds: build fixes %d, reviews %d, fallback attempts %d\n", g.BuildFixes, g.Reviews, g.Fallback)
		for _, k := range sortedKeys(g.WrittenBy) {
			fmt.Fprintf(w, "  %-40s %d\n", k, g.WrittenBy[k])
		}
	} else {
		fmt.Fprintln(w, "Git: no foreman commits")
	}
}

func writeOutcomes(w io.Writer, label string, o Outcomes) {
	if o.Total == 0 {
		return
	}
	fmt.Fprintf(w, "%s: %d calls, applied %d, failed %d, refused %d; local %d (%.0f%% of local-mode, first try %d), validator %d",
		label, o.Total, o.Applied, o.Failed, o.Refused, o.Local, 100*o.LocalShare(), o.FirstTry, o.Validator)
	if o.Direct > 0 {
		fmt.Fprintf(w, " (direct %d)", o.Direct)
	}
	fmt.Fprintf(w, "; rounds: build %d, review %d, fallback %d\n", o.BuildFixes, o.Reviews, o.Fallback)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ms(v int64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1fs", float64(v)/1000)
	}
	return fmt.Sprintf("%dms", v)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func cost(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("$%.4f", v)
}
