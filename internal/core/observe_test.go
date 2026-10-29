package core

import "testing"

func cfgWithRoleCmds(cmds map[string]string) Config {
	cfg := Config{Roles: map[string]Role{}, Projects: map[string]ProjectConfig{}}
	for name, cmd := range cmds {
		cfg.Roles[name] = Role{Cmd: cmd}
	}
	return cfg
}

func TestStateOfReliableSignals(t *testing.T) {
	setHostname(t, "testhost")
	fakeTmux(t, nil) // no capture output: heuristic defaults apply
	cfg := cfgWithRoleCmds(map[string]string{"coder": "claude"})
	cases := []struct {
		name string
		w    Window
		want State
	}{
		{"dead pane", Window{Role: "coder", Command: "zsh", Dead: true}, StateExited},
		{"role cmd finished", Window{Role: "coder", Command: "zsh"}, StateDone},
		{"numbered participant finished", Window{Role: "coder-2", Command: "zsh"}, StateDone},
		{"login shell counts too", Window{Role: "coder", Command: "-zsh"}, StateDone},
		{"manual terminal", Window{Role: "human", Command: "zsh"}, StateShell},
		{"unknown role at shell", Window{Role: "scratch", Command: "bash"}, StateShell},
		{"process running", Window{Role: "coder", Command: "2.1.200"}, StateWorking},
	}
	for _, c := range cases {
		if got := StateOf(cfg, c.w); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClassifyTitle(t *testing.T) {
	cases := []struct {
		title string
		want  State
	}{
		{"⠂ testing-foundation-sprint", StateWorking},
		{"⠐ another frame", StateWorking},
		{"✳ Review NamWeb UI and …", StateWaiting},
		{"", ""},
		{"my dev server", ""},
	}
	for _, c := range cases {
		if got := classifyTitle(c.title); got != c.want {
			t.Errorf("classifyTitle(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestClassifyOutput(t *testing.T) {
	cases := []struct {
		name, text string
		want       State
	}{
		{"selector cursor", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n", StateWaiting},
		{"y/n suffix", "Overwrite existing file? [y/n]\n", StateWaiting},
		{"parenthesised y/n", "continue? (Y/n)\n", StateWaiting},
		{"plain output", "compiling...\nlinking...\ndone in 3s\n", StateWorking},
		{"prompt scrolled into history", "❯ 1. Yes\n1\n2\n3\n4\n5\n6\n7\n", StateWorking},
		{"empty", "", StateWorking},
	}
	for _, c := range cases {
		if got := classifyOutput(c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStateOfTitleBeatsCapture(t *testing.T) {
	setHostname(t, "testhost")
	fakeTmux(t, nil) // capture-pane would fail: must not be consulted
	cfg := cfgWithRoleCmds(map[string]string{"coder": "claude"})
	w := Window{Role: "coder", Command: "2.1.200", Title: "✳ idle agent"}
	if got := StateOf(cfg, w); got != StateWaiting {
		t.Errorf("got %q, want waiting from title alone", got)
	}
	w.Title = "⠂ busy agent"
	if got := StateOf(cfg, w); got != StateWorking {
		t.Errorf("got %q, want working from title alone", got)
	}
}

func TestStateOfCaptureFallback(t *testing.T) {
	setHostname(t, "testhost")
	cfg := cfgWithRoleCmds(map[string]string{"coder": "aider"})
	// Title is the hostname default → uninformative → capture-pane decides.
	w := Window{ID: "@1", Role: "coder", Command: "aider", Title: "testhost"}
	fakeTmux(t, map[string]string{"capture-pane": "Apply changes? (y/n)"})
	if got := StateOf(cfg, w); got != StateWaiting {
		t.Errorf("got %q, want waiting from capture", got)
	}
	fakeTmux(t, map[string]string{"capture-pane": "thinking..."})
	if got := StateOf(cfg, w); got != StateWorking {
		t.Errorf("got %q, want working", got)
	}
	fakeTmux(t, nil) // capture fails (e.g. no server): default working
	if got := StateOf(cfg, w); got != StateWorking {
		t.Errorf("got %q, want working when capture fails", got)
	}
}

func TestLastStateRoundTrip(t *testing.T) {
	fakeTmux(t, map[string]string{"show-options": "waiting"})
	if got := LastState(Window{ID: "@1"}); got != StateWaiting {
		t.Errorf("got %q, want waiting", got)
	}
	fakeTmux(t, nil) // option unset / no server
	if got := LastState(Window{ID: "@1"}); got != "" {
		t.Errorf("got %q, want empty for unset", got)
	}
}

func TestTickerLine(t *testing.T) {
	setHostname(t, "testhost")
	cfg := cfgWithRoleCmds(map[string]string{"coder": "claude"})
	projects := []Project{
		{Name: "A", Windows: []Window{ // current project: skipped entirely
			{Role: "planner", Command: "2.1.200", Title: "✳ waiting here"},
		}},
		{Name: "B", Windows: []Window{
			{Role: "planner", Command: "2.1.200", Title: "✳ needs you"},
			{Role: "coder", Command: "2.1.200", Title: "⠂ busy"}, // working: quiet
			{Role: "human", Command: "zsh"},                      // shell: quiet
		}},
		{Name: "C", Windows: []Window{
			{Role: "coder", Command: "2.1.200", Title: "⠐ busy"},
		}},
		{Name: "D", Windows: []Window{
			{Role: "x", Command: "2.1.200", Title: "✳ a"},
			{Role: "y", Command: "2.1.200", Title: "✳ b"},
		}},
	}
	got := TickerLine(cfg, projects, "A")
	want := "B: planner⚠  D: x⚠ y⚠"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Nobody waiting anywhere else → empty line, quiet status bar.
	// (A's own waiting participant doesn't count from inside A.)
	if got := TickerLine(cfg, []Project{projects[0], projects[2]}, "A"); got != "" {
		t.Errorf("want empty when only current project waits, got %q", got)
	}
}

func TestTickerLineUsesFullClassifier(t *testing.T) {
	setHostname(t, "testhost")
	// A titleless agent at a y/n prompt must show in the ticker exactly as
	// it does in list and notifications — one classifier for all three
	// surfaces (issue #38).
	fakeTmux(t, map[string]string{"capture-pane": "Apply changes? (y/n)"})
	projects := []Project{{Name: "B", Windows: []Window{
		{ID: "@1", Role: "coder", Command: "aider", Title: "testhost"},
	}}}
	if got := TickerLine(Config{Roles: map[string]Role{}}, projects, "A"); got != "B: coder⚠" {
		t.Errorf("got %q, want the capture-detected waiter", got)
	}
}

func TestStateOfDoneUsesRoleNameFallback(t *testing.T) {
	// A window without @fm_role but whose *name* matches a preset role
	// still counts as done when back at the shell.
	cfg := cfgWithRoleCmds(map[string]string{"coder": "claude"})
	if got := StateOf(cfg, Window{Name: "coder", Command: "zsh"}); got != StateDone {
		t.Errorf("got %q, want done via RoleName fallback", got)
	}
}
