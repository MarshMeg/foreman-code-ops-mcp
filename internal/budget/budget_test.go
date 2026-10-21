package budget

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func words(_ context.Context, s string) (int, error) { return len(strings.Fields(s)), nil }

func parts() []Part {
	return []Part{
		{Key: "style", Priority: Style, Text: "s s s"},
		{Key: "outline", Priority: Outline, Text: "o o o o"},
		{Key: "ctx:a", Priority: ContextSymbols, Text: "a a a"},
		{Key: "ctx:b", Priority: ContextSymbols, Text: "b b b"},
		{Key: "imports", Priority: Imports, Text: "i i"},
		{Key: "task", Priority: Required, Text: "t t t t t"},
	}
}

func keys(ps []Part) string {
	var k []string
	for _, p := range ps {
		k = append(k, p.Key)
	}
	return strings.Join(k, ",")
}

func TestFitAll(t *testing.T) {
	r, err := Fit(context.Background(), parts(), 100, words)
	if err != nil || len(r.Dropped) != 0 || r.Tokens != 20 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestFitDropsInPriorityOrder(t *testing.T) {
	cases := map[int]string{
		18: "style,outline,ctx:a,ctx:b,task", // imports dropped
		14: "style,ctx:a,ctx:b,task",         // then outline
		11: "ctx:a,ctx:b,task",               // then style
		8:  "ctx:a,task",                     // then the last context symbol
		5:  "task",
	}
	for limit, want := range cases {
		r, err := Fit(context.Background(), parts(), limit, words)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if keys(r.Kept) != want || r.Tokens > limit {
			t.Errorf("limit %d: kept %s (%d tokens), want %s", limit, keys(r.Kept), r.Tokens, want)
		}
	}
}

func TestFitTooLarge(t *testing.T) {
	_, err := Fit(context.Background(), parts(), 4, words)
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err = %v", err)
	}
}
