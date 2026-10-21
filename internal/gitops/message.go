package gitops

import (
	"strings"
)

// Trailer keys written by foreman.
const (
	TrailerWrittenBy  = "Foreman-Written-By"
	TrailerReviewedBy = "Foreman-Reviewed-By"
	TrailerAttempts   = "Foreman-Attempts"
)

// Message is a foreman commit message:
//
//	foreman(svc1,svc2): subject
//
//	body
//
//	Trailer: value
type Message struct {
	Services []string
	Subject  string
	Body     string
	Trailers []Trailer
}

// Trailer is one "Key: value" line in the final paragraph.
type Trailer struct{ Key, Value string }

func (m Message) String() string {
	var b strings.Builder
	b.WriteString("foreman")
	if len(m.Services) > 0 {
		b.WriteString("(" + strings.Join(m.Services, ",") + ")")
	}
	b.WriteString(": ")
	b.WriteString(oneLine(m.Subject))
	b.WriteString("\n")
	if body := strings.TrimSpace(m.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	if len(m.Trailers) > 0 {
		b.WriteString("\n")
		for _, t := range m.Trailers {
			b.WriteString(t.Key + ": " + oneLine(t.Value) + "\n")
		}
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
