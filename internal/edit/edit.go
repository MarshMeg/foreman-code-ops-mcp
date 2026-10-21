// Package edit splices code into source by byte ranges. It keeps the indentation of the
// target position and normalizes blank lines around the change; full formatting is left
// to the language formatter.
package edit

import (
	"bytes"
	"fmt"
	"strings"
)

// Replace replaces src[start:end] with code, re-indented to the indentation at start.
func Replace(src []byte, start, end int, code string) ([]byte, error) {
	if err := checkRange(src, start, end); err != nil {
		return nil, err
	}
	code = reindent(code, indentAt(src, start))
	out := make([]byte, 0, len(src)-(end-start)+len(code))
	out = append(out, src[:start]...)
	out = append(out, code...)
	out = append(out, src[end:]...)
	return out, nil
}

// Append adds code as a new declaration at the end of src, after one blank line.
func Append(src []byte, code string) []byte {
	code = strings.Trim(code, "\n")
	body := bytes.TrimRight(src, "\n\t ")
	out := make([]byte, 0, len(body)+len(code)+3)
	out = append(out, body...)
	if len(body) > 0 {
		out = append(out, "\n\n"...)
	}
	out = append(out, code...)
	return append(out, '\n')
}

// InsertAfter adds code as a new declaration after the anchor src[start:end], separated
// by one blank line and indented like the anchor's first line.
func InsertAfter(src []byte, start, end int, code string) ([]byte, error) {
	if err := checkRange(src, start, end); err != nil {
		return nil, err
	}
	indent := lineIndent(src, start)
	code = reindent(code, indent)
	out := make([]byte, 0, len(src)+len(code)+len(indent)+2)
	out = append(out, src[:end]...)
	out = append(out, "\n\n"...)
	out = append(out, indent...)
	out = append(out, code...)
	out = append(out, src[end:]...)
	return out, nil
}

// Delete removes src[start:end] together with the lines it occupies when nothing else
// is on them, and collapses the blank lines left behind into one.
func Delete(src []byte, start, end int) ([]byte, error) {
	if err := checkRange(src, start, end); err != nil {
		return nil, err
	}
	ls := start
	for ls > 0 && (src[ls-1] == ' ' || src[ls-1] == '\t') {
		ls--
	}
	le := end
	for le < len(src) && (src[le] == ' ' || src[le] == '\t') {
		le++
	}
	if (ls == 0 || src[ls-1] == '\n') && (le == len(src) || src[le] == '\n') {
		start, end = ls, le
		if end < len(src) {
			end++ // the newline
		}
	}
	out := append(append([]byte{}, src[:start]...), src[end:]...)
	return collapseBlank(out, start), nil
}

func checkRange(src []byte, start, end int) error {
	if start < 0 || end < start || end > len(src) {
		return fmt.Errorf("edit: range [%d,%d) out of bounds (len %d)", start, end, len(src))
	}
	return nil
}

// collapseBlank reduces a run of blank lines around offset at to a single blank line,
// and drops blank lines left at the end of the file.
func collapseBlank(b []byte, at int) []byte {
	s := at
	for s > 0 && isSpace(b[s-1]) {
		s--
	}
	e := at
	for e < len(b) && isSpace(b[e]) {
		e++
	}
	gap := b[s:e]
	nl := bytes.Count(gap, []byte{'\n'})
	if nl <= 2 && e != len(b) {
		return b
	}
	var repl []byte
	switch {
	case e == len(b):
		repl = []byte("\n")
	case s == 0:
		repl = nil
	default:
		// Keep the indentation of the next line.
		lastNL := bytes.LastIndexByte(gap, '\n')
		repl = append([]byte("\n\n"), gap[lastNL+1:]...)
	}
	out := append(append([]byte{}, b[:s]...), repl...)
	return append(out, b[e:]...)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// indentAt returns the whitespace between the start of the line and offset at,
// or "" when non-whitespace precedes at on that line.
func indentAt(src []byte, at int) string {
	ls := at
	for ls > 0 && src[ls-1] != '\n' {
		ls--
	}
	prefix := src[ls:at]
	if len(bytes.TrimLeft(prefix, " \t")) != 0 {
		return ""
	}
	return string(prefix)
}

// lineIndent returns the leading whitespace of the line containing offset at.
func lineIndent(src []byte, at int) string {
	ls := at
	for ls > 0 && src[ls-1] != '\n' {
		ls--
	}
	le := ls
	for le < len(src) && (src[le] == ' ' || src[le] == '\t') {
		le++
	}
	return string(src[ls:le])
}

// reindent strips the common indentation of code and prefixes every line but the
// first with indent (the first line lands after existing indentation).
func reindent(code, indent string) string {
	code = strings.Trim(code, "\n")
	lines := strings.Split(code, "\n")
	common := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			common, first = lead, false
			continue
		}
		for !strings.HasPrefix(lead, common) {
			common = common[:len(common)-1]
		}
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		l = strings.TrimPrefix(l, common)
		if i > 0 {
			l = indent + l
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}
