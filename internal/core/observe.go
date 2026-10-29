package core

import "strings"

// State is what a participant is doing, observed as a snapshot — computed
// fresh from the tmux server on demand, never stored (see issue #1).
type State string

const (
	StateWorking State = "working" // agent busy
	StateWaiting State = "waiting" // agent wants attention
	StateDone    State = "done"    // role's command finished, back at the shell
	StateShell   State = "shell"   // manual terminal at its shell
	StateExited  State = "exited"  // pane dead
)

// StateOf observes one participant. The reliable signals: a dead pane, and
// "dropped back to the shell" — role commands run inside the shell, so the
// current command being a shell means the role's process ended (done) or the
// terminal never ran one (shell). There is exactly one classifier — list,
// ticker, and notifications must never disagree about who is waiting
// (issue #38); the capture fallback only fires for titleless agent windows,
// which are rare, so it's cheap enough for every caller.
func StateOf(cfg Config, w Window) State {
	if w.Dead {
		return StateExited
	}
	if shells[w.Command] {
		if r, _ := cfg.Role(w.RoleName()); r.Cmd != "" {
			return StateDone
		}
		return StateShell
	}
	// Agent-aware heuristic (issue #2): the pane title first — apps like
	// Claude Code set it deliberately, which beats guessing from content.
	if s := classifyTitle(paneTitle(w)); s != "" {
		return s
	}
	// Title uninformative: peek at the pane's bottom lines. Content matching
	// self-pollutes easily (output *mentioning* a prompt looks like one), so
	// the pattern set stays small and the default is working.
	if out, err := Tmux("capture-pane", "-p", "-t", w.ID); err == nil {
		if classifyOutput(out) == StateWaiting {
			return StateWaiting
		}
	}
	return StateWorking
}

// LastState reads the participant's previously recorded state — the watcher's
// memory, stored as a tmux window option so even it lives in the server
// (issue #5). Empty means never recorded.
func LastState(w Window) State {
	out, err := Tmux("show-options", "-w", "-q", "-v", "-t", w.ID, "@fm_last_state")
	if err != nil {
		return ""
	}
	return State(out)
}

// RecordState writes the watcher's memory back into the window.
func RecordState(w Window, s State) {
	Tmux("set-option", "-w", "-t", w.ID, "@fm_last_state", string(s))
}

// TickerLine renders the cross-project attention summary shown in a managed
// session's status bar: participants waiting in *other* projects, nothing
// else — empty when nobody needs you (issue #4). Projects arrive pinned-first
// from ListProjects and stay that way.
func TickerLine(cfg Config, projects []Project, current string) string {
	var parts []string
	for _, p := range projects {
		if p.Name == current {
			continue
		}
		var roles []string
		for _, w := range p.Windows {
			if StateOf(cfg, w) == StateWaiting {
				roles = append(roles, w.RoleName()+"⚠")
			}
		}
		if len(roles) > 0 {
			parts = append(parts, p.Name+": "+strings.Join(roles, " "))
		}
	}
	return strings.Join(parts, "  ")
}

// classifyTitle reads the state Claude Code (and friends) encode in the pane
// title: an animating braille spinner char while busy, ✳ when the agent
// wants attention. Empty result means the title says nothing about state.
func classifyTitle(title string) State {
	r := []rune(title)
	if len(r) == 0 {
		return ""
	}
	switch {
	case r[0] >= 0x2800 && r[0] <= 0x28FF: // braille spinner frames
		return StateWorking
	case r[0] == '✳':
		return StateWaiting
	}
	return ""
}

// classifyOutput inspects the tail of a pane for an input prompt. Only the
// last few visible lines count — anything older is history, not a prompt.
func classifyOutput(text string) State {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	from := len(lines) - 6
	if from < 0 {
		from = 0
	}
	for _, line := range lines[from:] {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "❯"), // selector cursor (permission menus)
			strings.HasSuffix(t, "[y/n]"), strings.HasSuffix(t, "[Y/n]"),
			strings.HasSuffix(t, "(y/n)"), strings.HasSuffix(t, "(Y/n)"):
			return StateWaiting
		}
	}
	return StateWorking
}
