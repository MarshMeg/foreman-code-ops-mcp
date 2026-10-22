package bench

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/providers"
	tu "github.com/MarshMeg/foreman-code-ops-mcp/internal/testutil"
)

func TestRun(t *testing.T) {
	llm := tu.NewFakeLLM(t)
	for range 6 {
		llm.AddRaw(tu.Verdict(true, "none", nil, nil))
	}
	p := providers.NewOpenAI(providers.OpenAIOptions{BaseURL: llm.URL, Model: "m"})
	r := Run(context.Background(), p, 6, 3)
	if r.Errors != 0 || r.P50 <= 0 || r.P95 < r.P50 {
		t.Fatalf("result = %+v", r)
	}
	if !strings.Contains(llm.Prompts()[0], "## Code to review") {
		t.Fatal("not a review prompt")
	}
	var b bytes.Buffer
	WriteMarkdown(&b, []Result{r})
	if !strings.Contains(b.String(), "| openai/m | 3 | 6/6 |") {
		t.Fatalf("table:\n%s", b.String())
	}
}
