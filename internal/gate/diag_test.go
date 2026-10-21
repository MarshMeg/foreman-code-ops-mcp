package gate

import (
	"regexp"
	"testing"
)

func TestParseGoBuildOutput(t *testing.T) {
	p := outputParser{
		re:          regexp.MustCompile(`^(?:vet: )?(?P<file>[^\s:#][^:]*\.go):(?P<line>\d+)(?::(?P<col>\d+))?: (?P<message>.*)$`),
		source:      "go build",
		severity:    SeverityError,
		projectRoot: "/p",
		cwd:         "/p/svc",
	}
	out := "# example.com/x/pkg\n" +
		"pkg/a.go:3:2: cannot use x (variable of type int) as string value\n" +
		"\thave (int)\n\twant (string)\n" +
		"vet: ./pkg/b.go:10: something\n" +
		"go: updates to go.mod needed\n"
	ds := p.parse(out)
	if len(ds) != 3 {
		t.Fatalf("got %+v", ds)
	}
	if ds[0].File != "svc/pkg/a.go" || ds[0].Line != 3 || ds[0].Column != 2 || ds[0].Message != "cannot use x (variable of type int) as string value\n\thave (int)\n\twant (string)" {
		t.Errorf("first = %+v", ds[0])
	}
	if ds[1].File != "svc/pkg/b.go" || ds[1].Line != 10 || ds[1].Column != 0 {
		t.Errorf("second = %+v", ds[1])
	}
	if ds[2].File != "" || ds[2].Message != "go: updates to go.mod needed" {
		t.Errorf("loose = %+v", ds[2])
	}
}

func TestParseGoImport(t *testing.T) {
	cases := map[string][2]string{
		`"context"`:         {"", "context"},
		`context`:           {"", "context"},
		`import "net/http"`: {"", "net/http"},
		`errs "errors"`:     {"errs", "errors"},
		`import _ "embed"`:  {"_", "embed"},
		"`github.com/a/b`":  {"", "github.com/a/b"},
	}
	for in, want := range cases {
		name, path, err := parseGoImport(in)
		if err != nil || name != want[0] || path != want[1] {
			t.Errorf("%s: got %q %q %v", in, name, path, err)
		}
	}
	for _, bad := range []string{"", `"unterminated`, "a b c"} {
		if _, _, err := parseGoImport(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestExpand(t *testing.T) {
	got := expand([]string{"go", "build", "-overlay", "{overlay}", "-o", "{tmp}/", "{packages}"},
		map[string][]string{"{overlay}": {"/t/o.json"}, "{tmp}": {"/t"}, "{packages}": {"./a", "./b"}})
	if want := "go build -overlay /t/o.json -o /t/ ./a ./b"; joinArgs(got) != want {
		t.Fatalf("got %q", joinArgs(got))
	}
	if got := expand([]string{"x", "{packages}"}, map[string][]string{"{packages}": nil}); joinArgs(got) != "x" {
		t.Fatalf("empty expansion: %q", got)
	}
}

func joinArgs(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}
