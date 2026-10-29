package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOREMAN_CONFIG", path)
}

func TestLoadConfig(t *testing.T) {
	writeConfig(t, `
# a comment
[role.planner]
cmd = "claude --permission-mode plan"

[role.server]
cmd = "npm run dev"
dir = "~/code/a"

[project.A]
root = "~/code/project-a"

[terminal]
app = "terminal"
`)
	cfg := LoadConfig()
	if got := cfg.Roles["planner"].Cmd; got != "claude --permission-mode plan" {
		t.Errorf("planner cmd = %q", got)
	}
	if r := cfg.Roles["server"]; r.Cmd != "npm run dev" || r.Dir != "~/code/a" {
		t.Errorf("server role = %+v", r)
	}
	if got := cfg.Projects["A"].Root; got != "~/code/project-a" {
		t.Errorf("project A root = %q", got)
	}
	if cfg.Terminal != "terminal" {
		t.Errorf("terminal app = %q", cfg.Terminal)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	t.Setenv("FOREMAN_CONFIG", filepath.Join(t.TempDir(), "nope.toml"))
	cfg := LoadConfig()
	if cfg.Roles == nil || cfg.Projects == nil {
		t.Fatal("maps must be non-nil for a missing config")
	}
	if len(cfg.Roles) != 0 || len(cfg.Projects) != 0 {
		t.Errorf("want empty config, got %+v", cfg)
	}
}

func TestLoadConfigTolerance(t *testing.T) {
	writeConfig(t, `
not a key value line
[nosection]
stray = "ignored"
[role.x]
cmd = unquoted value
`)
	cfg := LoadConfig()
	if got := cfg.Roles["x"].Cmd; got != "unquoted value" {
		t.Errorf("unquoted value: got %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	cases := map[string]string{
		"~/code/a":  "/home/test/code/a",
		"/absolute": "/absolute",
		"relative":  "relative",
	}
	for in, want := range cases {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkDirPrecedence(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	cfg := Config{
		Roles:    map[string]Role{"withdir": {Dir: "~/roledir"}, "plain": {}},
		Projects: map[string]ProjectConfig{"A": {Root: "~/projroot"}},
	}
	// Role dir beats project root.
	if got := cfg.WorkDir("A", "withdir"); got != "/home/test/roledir" {
		t.Errorf("role dir: got %q", got)
	}
	// Project root beats cwd.
	if got := cfg.WorkDir("A", "plain"); got != "/home/test/projroot" {
		t.Errorf("project root: got %q", got)
	}
	// Neither configured: the caller's cwd.
	wd, _ := os.Getwd()
	if got := cfg.WorkDir("B", "plain"); got != wd {
		t.Errorf("cwd fallback: got %q, want %q", got, wd)
	}
}

func TestRoleResolvesNumberedParticipants(t *testing.T) {
	cfg := Config{Roles: map[string]Role{"coder": {Cmd: "claude"}, "coder-2": {Cmd: "codex"}}}
	cases := map[string]string{"coder": "claude", "coder-2": "codex", "coder-3": "claude", "coder-2-2": "codex", "coder-x": "", "-2": "", "coder-": ""}
	for name, want := range cases {
		if r, _ := cfg.Role(name); r.Cmd != want {
			t.Errorf("Role(%q).Cmd = %q, want %q", name, r.Cmd, want)
		}
	}
}
