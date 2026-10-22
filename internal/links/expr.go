package links

import (
	"regexp"
	"strings"
)

// argument returns the text of the call argument that starts at src[at:], up to the first
// top-level comma or the closing parenthesis.
func argument(src string, at int) string {
	depth := 0
	var quote byte
	for i := at; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return strings.TrimSpace(src[at:i])
			}
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(src[at:i])
			}
		}
	}
	return strings.TrimSpace(src[at:])
}

// callTail returns the rest of the call after its first argument, up to the closing paren.
func callTail(src string, at int) string {
	depth := 0
	var quote byte
	for i := at; i < len(src) && i < at+2000; i++ {
		c := src[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return src[at:i]
			}
			depth--
		}
	}
	return ""
}

var (
	stringLit = regexp.MustCompile("(?s)[rfbu]{0,2}\"(?:[^\"\\\\]|\\\\.)*\"|[rfbu]{0,2}'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")
)

// urlExpr turns a URL expression into a path template: string literal pieces are kept,
// everything else (variables, function calls, interpolations) becomes "{}". It also returns
// the first non-literal piece, which is usually the base URL.
//
//	baseURL + "/books/" + id         -> {}/books/{}, baseURL
//	`${API}/books/${id}`             -> {}/books/{}, API
//	f"{BASE}/books/{book_id}"        -> {}/books/{}, BASE
//	fmt.Sprintf("%s/books/%d", b, i) -> %s/books/%d (normalized later), b
func urlExpr(expr string) (template, base string, dynamic bool) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "fmt.Sprintf(") {
		inner := expr[len("fmt.Sprintf("):]
		format := argument(inner, 0)
		rest := strings.TrimPrefix(inner[len(format):], ",")
		lit := unquote(format)
		if strings.HasPrefix(lit, "%s") || strings.HasPrefix(lit, "%v") {
			base = strings.TrimSpace(argument(rest, 0))
		}
		return lit, base, strings.Contains(lit, "%")
	}
	var b strings.Builder
	last := 0
	for _, loc := range stringLit.FindAllStringIndex(expr, -1) {
		if gap := strings.Trim(expr[last:loc[0]], " +\t\n"); gap != "" {
			if base == "" && b.Len() == 0 {
				base = gap
			}
			b.WriteString("{}")
			dynamic = true
		}
		lit := expr[loc[0]:loc[1]]
		inner := unquote(lit)
		if strings.HasPrefix(lit, "`") || strings.HasPrefix(strings.TrimLeft(lit, "rbu"), "f") {
			if b.Len() == 0 && base == "" {
				if m := interp.FindStringSubmatchIndex(inner); m != nil && m[0] == 0 {
					base = inner[m[2]:m[3]]
				}
			}
			if interp.MatchString(inner) {
				dynamic = true
			}
			inner = interp.ReplaceAllString(inner, "{}")
		}
		b.WriteString(inner)
		last = loc[1]
	}
	if gap := strings.Trim(expr[last:], " +\t\n"); gap != "" {
		if b.Len() == 0 {
			base = gap
		}
		b.WriteString("{}")
		dynamic = true
	}
	return b.String(), base, dynamic
}

var interp = regexp.MustCompile(`\$?\{([^}]*)\}`)

func unquote(lit string) string {
	lit = strings.TrimLeft(lit, "rfbu")
	if len(lit) >= 2 {
		return lit[1 : len(lit)-1]
	}
	return lit
}

// stripBase removes a leading "{}" that stands for the base URL ("{}/books" -> "/books").
func stripBase(template string) string {
	for _, p := range []string{"{}", "%s", "%v"} {
		if strings.HasPrefix(template, p+"/") {
			return template[len(p):]
		}
	}
	return template
}

// envVar extracts an environment variable name from a base URL expression.
var envVarRe = regexp.MustCompile(`(?:import\.meta\.env|process\.env)\.([A-Za-z_]\w*)|os\.Getenv\("([A-Za-z_]\w*)"\)|os\.(?:environ\.get|getenv)\(\s*["']([A-Za-z_]\w*)["']|os\.environ\[\s*["']([A-Za-z_]\w*)["']\s*\]`)

func envVar(expr string) string {
	m := envVarRe.FindStringSubmatch(expr)
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// lineOf returns the 1-based line of offset in src.
func lineOf(src string, offset int) int { return strings.Count(src[:offset], "\n") + 1 }

// blockEnd returns the offset of the brace closing the block opened at or after at.
func blockEnd(src string, at int) int {
	open := strings.IndexByte(src[at:], '{')
	if open < 0 {
		return len(src)
	}
	depth := 0
	for i := at + open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(src)
}
