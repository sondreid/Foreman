package core

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Project is the live state of one project: its participants and how it is
// being viewed. Pinned projects sort first.
type Project struct {
	Name     string
	Pinned   bool
	Attached bool
	Clients  int
	Windows  []Window
}

// ListProjects returns all foreman-managed projects, pinned first. A missing
// tmux server is not an error — it just means no projects.
func ListProjects() ([]Project, error) {
	out, err := Tmux("list-sessions", "-F", "#{session_name}\t#{@fm_priority}\t#{session_attached}")
	if err != nil {
		return nil, nil
	}
	var projects []Project
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 || !strings.HasPrefix(f[0], SessionPrefix) {
			continue
		}
		name := strings.TrimPrefix(f[0], SessionPrefix)
		ws, _ := ListWindows(name)
		clients, _ := strconv.Atoi(f[2])
		projects = append(projects, Project{Name: name, Pinned: f[1] == "1", Attached: clients > 0, Clients: clients, Windows: ws})
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].Pinned && !projects[j].Pinned
	})
	return projects, nil
}

var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true, "ksh": true, "-zsh": true, "-bash": true}

// WindowStatus describes what a participant is doing, for humans.
func WindowStatus(w Window) string {
	if w.Dead {
		return "exited"
	}
	if shells[w.Command] {
		return "shell"
	}
	// Apps that announce themselves via the pane title (Claude Code sets
	// "✳ Claude Code") beat the process name, which can be meaningless —
	// Claude Code's native binary is named after its version ("2.1.200").
	if t := paneTitle(w); t != "" {
		return t
	}
	return w.Command
}

// paneTitle returns the pane title if an application actually set it;
// untouched panes carry the hostname as tmux's default.
func paneTitle(w Window) string {
	t := strings.TrimSpace(w.Title)
	if t == "" || t == hostname() {
		return ""
	}
	if r := []rune(t); len(r) > 24 {
		t = string(r[:23]) + "…"
	}
	return t
}

var cachedHostname string

func hostname() string {
	if cachedHostname == "" {
		cachedHostname, _ = os.Hostname()
	}
	return cachedHostname
}

// HumanAge renders a tmux unix timestamp as a compact age like "2m".
func HumanAge(unixStr string) string {
	ts, err := strconv.ParseInt(unixStr, 10, 64)
	if err != nil || ts == 0 {
		return "-"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
