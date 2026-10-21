package gate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Diagnostic is a normalized finding. File is relative to the project root when the
// finding is inside the project.
type Diagnostic struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity" jsonschema:"error or warning"`
	Source   string `json:"source" jsonschema:"step that reported it, e.g. go build"`
	Message  string `json:"message"`
}

func (d Diagnostic) String() string {
	loc := d.File
	if d.Line > 0 {
		loc += ":" + strconv.Itoa(d.Line)
		if d.Column > 0 {
			loc += ":" + strconv.Itoa(d.Column)
		}
	}
	if loc != "" {
		loc += ": "
	}
	return fmt.Sprintf("%s%s (%s)", loc, d.Message, d.Source)
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

const (
	SeverityError   = "error"
	SeverityWarning = "warning"

	maxDiagnostics = 50
	maxMessage     = 2000
)

// outputParser turns tool output into diagnostics.
type outputParser struct {
	re       *regexp.Regexp
	source   string
	severity string
	// projectRoot and cwd are absolute; file paths in output are resolved against cwd.
	projectRoot, cwd string
	// backing maps overlay backing files (which tools may report) to project-relative paths.
	backing map[string]string
	// shadow is a shadow root standing in for realDir.
	shadow, realDir string
	// onlyMatches drops lines that do not match re.
	onlyMatches bool
	// format selects a structured parser ("eslint-json").
	format string
}

// parse extracts diagnostics line by line. Lines matching the pattern start a diagnostic,
// indented or other non-matching lines continue the previous one, and package headers
// ("# pkg") are dropped. Output with no match at all becomes a single file-less diagnostic.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (p outputParser) parse(output string) []Diagnostic {
	output = ansi.ReplaceAllString(output, "")
	if p.format == "eslint-json" {
		if ds, ok := p.parseESLint(output); ok {
			return ds
		}
	}
	var out []Diagnostic
	var loose []string
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		if p.re != nil {
			if m := p.re.FindStringSubmatch(line); m != nil {
				out = append(out, p.fromMatch(m))
				continue
			}
		}
		if p.onlyMatches {
			continue
		}
		if len(out) > 0 && (strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ")) {
			last := &out[len(out)-1]
			last.Message += "\n" + strings.TrimRight(line, " ")
			continue
		}
		loose = append(loose, line)
	}
	if len(loose) > 0 {
		out = append(out, Diagnostic{Severity: p.severity, Source: p.source, Message: strings.Join(loose, "\n")})
	}
	for i := range out {
		if len(out[i].Message) > maxMessage {
			out[i].Message = out[i].Message[:maxMessage] + "…"
		}
	}
	return out
}

func (p outputParser) fromMatch(m []string) Diagnostic {
	d := Diagnostic{Severity: p.severity, Source: p.source}
	for i, name := range p.re.SubexpNames() {
		switch name {
		case "file":
			d.File = p.relFile(m[i])
		case "line":
			d.Line, _ = strconv.Atoi(m[i])
		case "col":
			d.Column, _ = strconv.Atoi(m[i])
		case "message":
			d.Message = m[i]
		case "severity":
			if strings.Contains(strings.ToLower(m[i]), "warn") {
				d.Severity = SeverityWarning
			}
		}
	}
	return d
}

func (p outputParser) relFile(f string) string {
	if !filepath.IsAbs(f) {
		f = filepath.Join(p.cwd, f)
	}
	f = filepath.Clean(f)
	if rel, ok := p.backing[f]; ok {
		return rel
	}
	if p.shadow != "" && (f == p.shadow || strings.HasPrefix(f, p.shadow+string(filepath.Separator))) {
		f = filepath.Join(p.realDir, strings.TrimPrefix(f, p.shadow))
	}
	if rel, err := filepath.Rel(p.projectRoot, f); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return f
}

// cap limits the number of diagnostics, noting how many were dropped.
func capDiagnostics(ds []Diagnostic) []Diagnostic {
	if len(ds) <= maxDiagnostics {
		return ds
	}
	dropped := len(ds) - maxDiagnostics
	ds = ds[:maxDiagnostics]
	return append(ds, Diagnostic{Severity: SeverityWarning, Source: "gate", Message: fmt.Sprintf("%d more diagnostics omitted", dropped)})
}

// parseESLint reads `eslint --format json` output.
func (p outputParser) parseESLint(output string) ([]Diagnostic, bool) {
	start := strings.IndexByte(output, '[')
	if start < 0 {
		return nil, false
	}
	var files []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID   string `json:"ruleId"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(output[start:]), &files); err != nil {
		return nil, false
	}
	var out []Diagnostic
	for _, f := range files {
		for _, m := range f.Messages {
			sev := SeverityWarning
			if m.Severity >= 2 {
				sev = SeverityError
			}
			msg := m.Message
			if m.RuleID != "" {
				msg += " (" + m.RuleID + ")"
			}
			out = append(out, Diagnostic{File: p.relFile(f.FilePath), Line: m.Line, Column: m.Column, Severity: sev, Source: p.source, Message: msg})
		}
	}
	return out, true
}
