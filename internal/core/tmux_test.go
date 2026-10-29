package core

import (
	"fmt"
	"strings"
	"testing"
)

// fakeTmux substitutes tmuxRun with canned output keyed on the tmux
// subcommand (args[0]); unmapped subcommands fail like a real tmux error.
// Tests using it must not run in parallel.
func fakeTmux(t *testing.T, responses map[string]string) {
	t.Helper()
	orig := tmuxRun
	t.Cleanup(func() { tmuxRun = orig })
	tmuxRun = func(args ...string) ([]byte, error) {
		if out, ok := responses[args[0]]; ok {
			return []byte(out + "\n"), nil
		}
		return []byte("unknown command"), fmt.Errorf("exit 1")
	}
}

func TestTmuxTrimsOutput(t *testing.T) {
	fakeTmux(t, map[string]string{"display-message": "  hello  "})
	out, err := Tmux("display-message", "-p", "x")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}
}

func TestTmuxErrorIncludesCommandAndOutput(t *testing.T) {
	fakeTmux(t, nil)
	_, err := Tmux("kill-session", "-t", "x")
	if err == nil {
		t.Fatal("want error")
	}
	if got := err.Error(); got != "tmux kill-session: unknown command" {
		t.Errorf("got %q", got)
	}
}

func TestSessionName(t *testing.T) {
	if got := SessionName("A"); got != "fm-A" {
		t.Errorf("got %q", got)
	}
}

func TestSessionExists(t *testing.T) {
	fakeTmux(t, map[string]string{"has-session": ""})
	if !SessionExists("A") {
		t.Error("want true when has-session succeeds")
	}
	fakeTmux(t, nil)
	if SessionExists("A") {
		t.Error("want false when has-session fails")
	}
}

func TestListWindowsParsing(t *testing.T) {
	fakeTmux(t, map[string]string{"list-windows": strings.Join([]string{
		"@1\tcoder\tcoder\tzsh\thost\t100\t0",
		"@2\t\tserver\tnpm\tmy dev server\t200\t1",
		"short\tline", // malformed: skipped
	}, "\n")})
	ws, err := ListWindows("A")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d windows, want 2", len(ws))
	}
	w := ws[0]
	if w.ID != "@1" || w.Role != "coder" || w.Command != "zsh" || w.Dead {
		t.Errorf("window 0 parsed wrong: %+v", w)
	}
	if !ws[1].Dead || ws[1].Title != "my dev server" {
		t.Errorf("window 1 parsed wrong: %+v", ws[1])
	}
}

func TestFindWindowPrefersRoleOverName(t *testing.T) {
	// @1's *name* is coder but its role is planner; @2's *role* is coder.
	fakeTmux(t, map[string]string{"list-windows": strings.Join([]string{
		"@1\tplanner\tcoder\tzsh\t\t100\t0",
		"@2\tcoder\trenamed\tzsh\t\t100\t0",
	}, "\n")})
	w, err := FindWindow("A", "coder")
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != "@2" {
		t.Errorf("got %s, want @2 (role match must beat name match)", w.ID)
	}
	// Name is still a fallback when no role matches.
	w, err = FindWindow("A", "renamed")
	if err != nil || w.ID != "@2" {
		t.Errorf("name fallback: got %+v, %v", w, err)
	}
}

func TestFindWindowErrorListsAvailable(t *testing.T) {
	fakeTmux(t, map[string]string{"list-windows": "@1\tplanner\tplanner\tzsh\t\t100\t0"})
	_, err := FindWindow("A", "coder")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "planner") {
		t.Errorf("error should list available roles: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":                    "'plain'",
		"My Project":               "'My Project'",
		"x; rm -rf ~":              "'x; rm -rf ~'",
		`back"quote`:               `'back"quote'`,
		"it's":                     `'it'\''s'`,
		"$(whoami)":                "'$(whoami)'",
		"/Users/First Last/bin/fm": "'/Users/First Last/bin/fm'",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestProjectColorStable(t *testing.T) {
	a, b := ProjectColor("NamWeb"), ProjectColor("NamWeb")
	if a != b || a == "" {
		t.Errorf("color not stable or empty: %q vs %q", a, b)
	}
}

func TestFreeRoleNameNumbersDuplicates(t *testing.T) {
	ws := []Window{{Role: "coder"}, {Role: "coder-2"}, {Name: "coder-3"}}
	cfg := Config{Roles: map[string]Role{"coder": {}, "coder-4": {Cmd: "other"}, "planner": {}}}
	if got := FreeRoleName(ws, cfg, "coder"); got != "coder-5" {
		t.Errorf("got %q, want coder-5 (configured coder-4 is not free)", got)
	}
	if got := FreeRoleName(ws, cfg, "planner"); got != "planner" {
		t.Errorf("got %q, want planner", got)
	}
}
