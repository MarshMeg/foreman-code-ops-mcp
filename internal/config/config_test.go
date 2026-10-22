package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFilesGivesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{GlobalPath: filepath.Join(dir, "nope.yaml"), ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("got %+v, want defaults", cfg)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yaml")
	writeFile(t, global, `
generator:
  model: global-model
  slots: 2
limits:
  max_reviews: 4
  max_build_fixes: 7
validator:
  timeout: 30s
`)
	writeFile(t, ProjectConfigPath(dir), `
generator:
  model: project-model
limits:
  max_reviews: 1
`)
	cfg, err := Load(Options{
		GlobalPath: global,
		ProjectDir: dir,
		LookupEnv:  envMap(map[string]string{"FOREMAN_GENERATOR_MODEL": "env-model", "FOREMAN_MAX_BUILD_FIXES": "5"}),
		Flags:      map[string]string{"generator.model": "cli-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"model from CLI", cfg.Generator.Model, "cli-model"},
		{"slots from global", cfg.Generator.Slots, 2},
		{"max_reviews from project", cfg.Limits.MaxReviews, 1},
		{"max_build_fixes from env", cfg.Limits.MaxBuildFixes, 5},
		{"timeout from global", cfg.Validator.Timeout.D(), 30 * time.Second},
		{"untouched default", cfg.Generator.SlotCtx, 65536},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, ProjectConfigPath(dir), "generator:\n  bogus_key: typo\n")
	_, err := Load(Options{ProjectDir: dir})
	if err == nil || !strings.Contains(err.Error(), "bogus_key") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, ProjectConfigPath(dir), "")
	if _, err := Load(Options{ProjectDir: dir}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCollectsErrors(t *testing.T) {
	c := Default()
	c.Generator.Provider = "bogus"
	c.Generator.SlotCtx = 100
	c.Git.Dirty = "maybe"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"generator.provider", "slot_ctx", "git.dirty"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestValidatorNoneNeedsNoModel(t *testing.T) {
	c := Default()
	c.Validator.Provider = "none"
	c.Validator.Model = ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBadValues(t *testing.T) {
	for _, flags := range []map[string]string{
		{"generator.slots": "three"},
		{"validator.timeout": "soon"},
		{"no.such": "x"},
	} {
		if _, err := Load(Options{Flags: flags}); err == nil {
			t.Errorf("flags %v: expected error", flags)
		}
	}
}

func TestNames(t *testing.T) {
	cases := map[string][2]string{
		"limits.max_reviews": {"max-reviews", "FOREMAN_MAX_REVIEWS"},
		"generator.provider": {"generator-provider", "FOREMAN_GENERATOR_PROVIDER"},
		"generator.slot_ctx": {"generator-slot-ctx", "FOREMAN_GENERATOR_SLOT_CTX"},
	}
	for key, want := range cases {
		if got := FlagName(key); got != want[0] {
			t.Errorf("FlagName(%s) = %s, want %s", key, got, want[0])
		}
		if got := EnvName(key); got != want[1] {
			t.Errorf("EnvName(%s) = %s, want %s", key, got, want[1])
		}
	}
}

func TestRegisterFlagsOnlyReturnsSetFlags(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	get := RegisterFlags(fs)
	if err := fs.Parse([]string{"--max-reviews", "3", "--validator-provider=none"}); err != nil {
		t.Fatal(err)
	}
	got := get()
	if len(got) != 2 || got["limits.max_reviews"] != "3" || got["validator.provider"] != "none" {
		t.Fatalf("got %v", got)
	}
}

func TestLimitsWith(t *testing.T) {
	n := 9
	d := Duration(time.Second)
	l := Default().Limits.With(&LimitsOverride{MaxReviews: &n, CheckTimeout: &d})
	if l.MaxReviews != 9 || l.CheckTimeout.D() != time.Second || l.MaxBuildFixes != 3 {
		t.Fatalf("got %+v", l)
	}
	if Default().Limits.With(nil) != Default().Limits {
		t.Fatal("nil override changed limits")
	}
}

func TestExampleConfigMatchesDefaults(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, ProjectConfigPath(dir), string(data))
	cfg, err := Load(Options{ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("config.example.yaml drifted from Default():\n got %+v\nwant %+v", cfg, Default())
	}
}

func TestDocsListEverySetting(t *testing.T) {
	data, err := os.ReadFile("../../docs/config.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, key := range Keys() {
		row := "| `" + key + "` | `--" + FlagName(key) + "` | `" + EnvName(key) + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("docs/config.md is missing row %s", row)
		}
	}
}
