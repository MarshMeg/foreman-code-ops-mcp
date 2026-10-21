package edit

import (
	"strings"
	"testing"
)

func span(src, sub string) (int, int) {
	i := strings.Index(src, sub)
	if i < 0 {
		panic("missing " + sub)
	}
	return i, i + len(sub)
}

func TestReplaceTopLevel(t *testing.T) {
	src := "package p\n\nfunc A() {}\n\nfunc B() {}\n"
	s, e := span(src, "func A() {}")
	out, err := Replace([]byte(src), s, e, "func A() int {\n\treturn 1\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "package p\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() {}\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestReplaceIndented(t *testing.T) {
	src := "class C:\n    def a(self):\n        pass\n\n    def b(self):\n        pass\n"
	s, e := span(src, "def a(self):\n        pass")
	out, err := Replace([]byte(src), s, e, "def a(self):\n    return 1")
	if err != nil {
		t.Fatal(err)
	}
	want := "class C:\n    def a(self):\n        return 1\n\n    def b(self):\n        pass\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestReplaceDedentsCode(t *testing.T) {
	src := "class C:\n    def a(self):\n        pass\n"
	s, e := span(src, "def a(self):\n        pass")
	out, _ := Replace([]byte(src), s, e, "    def a(self):\n        return 2\n")
	if want := "class C:\n    def a(self):\n        return 2\n"; string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestInsertAfterAnchor(t *testing.T) {
	src := "package p\n\nfunc A() {}\n\nfunc B() {}\n"
	st, e := span(src, "func A() {}")
	out, _ := InsertAfter([]byte(src), st, e, "func N() {}")
	want := "package p\n\nfunc A() {}\n\nfunc N() {}\n\nfunc B() {}\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestInsertAfterIndentedAnchor(t *testing.T) {
	src := "class C:\n    def a(self):\n        pass\n"
	st, e := span(src, "def a(self):\n        pass")
	out, _ := InsertAfter([]byte(src), st, e, "def b(self):\n    return 1")
	want := "class C:\n    def a(self):\n        pass\n\n    def b(self):\n        return 1\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestAppend(t *testing.T) {
	for _, src := range []string{"package p\n", "package p\n\n\n", "package p"} {
		if out := Append([]byte(src), "\nfunc N() {}\n\n"); string(out) != "package p\n\nfunc N() {}\n" {
			t.Errorf("%q: got %q", src, out)
		}
	}
	if out := Append(nil, "x"); string(out) != "x\n" {
		t.Errorf("empty: got %q", out)
	}
}

func TestDeleteMiddle(t *testing.T) {
	src := "package p\n\nfunc A() {}\n\n// B doc\nfunc B() {}\n\nfunc C() {}\n"
	s, e := span(src, "// B doc\nfunc B() {}")
	out, _ := Delete([]byte(src), s, e)
	if want := "package p\n\nfunc A() {}\n\nfunc C() {}\n"; string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestDeleteLast(t *testing.T) {
	src := "package p\n\nfunc A() {}\n\nfunc B() {}\n"
	s, e := span(src, "func B() {}")
	out, _ := Delete([]byte(src), s, e)
	if want := "package p\n\nfunc A() {}\n"; string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestDeleteIndented(t *testing.T) {
	src := "type (\n\tA int\n\tB int\n)\n"
	s, e := span(src, "A int")
	out, _ := Delete([]byte(src), s, e)
	if want := "type (\n\tB int\n)\n"; string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestBadRange(t *testing.T) {
	if _, err := Replace([]byte("ab"), 1, 5, ""); err == nil {
		t.Fatal("expected error")
	}
}
