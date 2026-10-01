package core

import (
	"errors"
	"strings"
	"testing"
)

// fakeLinuxLaunch serves a PATH holding only the named binaries and records
// each launch as one joined argv line; restored via t.Cleanup.
func fakeLinuxLaunch(t *testing.T, onPath ...string) *[]string {
	t.Helper()
	fixGOOS(t, "linux")
	var launches []string
	origL, origS := lookPath, startTerminal
	t.Cleanup(func() { lookPath, startTerminal = origL, origS })
	lookPath = func(name string) (string, error) {
		for _, p := range onPath {
			if p == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
	startTerminal = func(path string, args ...string) error {
		launches = append(launches, path+" "+strings.Join(args, " "))
		return nil
	}
	return &launches
}

func TestOpenTerminalLinuxAutoDetectOrder(t *testing.T) {
	// The first entry on PATH wins: ptyxis before gnome-terminal.
	t.Setenv("PATH", "/usr/bin:/home/a/.local/bin")
	launches := fakeLinuxLaunch(t, "gnome-terminal", "ptyxis")
	if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err != nil {
		t.Fatal(err)
	}
	want := "/usr/bin/ptyxis --new-window -- sh -c unset TMUX TMUX_PANE; PATH='/usr/bin:/home/a/.local/bin' 'fm' 'A' go 'coder' || read -r _"
	if len(*launches) != 1 || (*launches)[0] != want {
		t.Errorf("launches = %q, want %q", *launches, want)
	}
}

func TestOpenTerminalLinuxConfiguredApp(t *testing.T) {
	launches := fakeLinuxLaunch(t, "ptyxis", "konsole")
	if err := OpenTerminal("konsole", "'fm' 'A' go 'coder'"); err != nil {
		t.Fatal(err)
	}
	if len(*launches) != 1 || !strings.HasPrefix((*launches)[0], "/usr/bin/konsole --separate -e sh -c ") {
		t.Errorf("launches = %q", *launches)
	}
}

func TestOpenTerminalLinuxRejectsUnknownApp(t *testing.T) {
	// "terminal" is Terminal.app — a macOS config carried over must fail
	// loudly, not open whatever is on PATH.
	launches := fakeLinuxLaunch(t, "ptyxis")
	err := OpenTerminal("terminal", "'fm' 'A' go 'coder'")
	if err == nil || !strings.Contains(err.Error(), "have: xdg-terminal-exec, ptyxis") {
		t.Errorf("err = %v", err)
	}
	if len(*launches) != 0 {
		t.Errorf("unknown app must not launch, got %q", *launches)
	}
}

func TestOpenTerminalLinuxNoneOnPath(t *testing.T) {
	_ = fakeLinuxLaunch(t)
	if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err == nil || !strings.Contains(err.Error(), "no terminal found") {
		t.Errorf("err = %v", err)
	}
	if err := OpenTerminal("ghostty", "'fm' 'A' go 'coder'"); err == nil || !strings.Contains(err.Error(), "tried: ghostty") {
		t.Errorf("configured but missing: err = %v", err)
	}
}
