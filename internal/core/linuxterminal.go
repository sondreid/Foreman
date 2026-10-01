package core

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// linuxTerminal is a terminal foreman can open a view in on Linux: its
// binary and the arguments that put it in a new window running a command.
type linuxTerminal struct {
	name   string
	prefix []string
}

// linuxTerminals in auto-detect order, used when [terminal] app is unset:
// xdg-terminal-exec honours the user's chosen default terminal where the
// distro ships it (Ubuntu 26.04); ptyxis is Fedora's and newer Ubuntu's
// default, gnome-terminal Ubuntu 24.04's, konsole KDE's.
var linuxTerminals = []linuxTerminal{
	{"xdg-terminal-exec", nil},
	{"ptyxis", []string{"--new-window", "--"}},
	{"gnome-terminal", []string{"--window", "--"}},
	{"konsole", []string{"--separate", "-e"}},
	{"ghostty", []string{"-e"}},
}

// lookPath and startTerminal are seams so tests can fake the PATH and the
// launch, like tmuxRun and runOsascript.
var lookPath = exec.LookPath

// startTerminal launches the terminal in its own session, so closing the
// shell that ran `go --window` doesn't take the window with it, and reaps it
// in the background, so the explorer neither blocks on a terminal that stays
// in the foreground (konsole, ghostty) nor collects zombies.
var startTerminal = func(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// openLinuxTerminal runs shellCmd through sh in a new window of app, or of
// the first linuxTerminals entry on PATH when app is unset. On success the
// command ends with its tmux attach and the window closes; a failing fm —
// or an attach ended by kill-server, which exits non-zero — keeps the window
// open until Enter, so the error stays readable. Windows are not tracked:
// there is no portable way to map one to its tty.
//
// The environment travels inside the command, not the process: ptyxis and
// gnome-terminal hand the command to an already-running instance that
// spawns it with its own environment. So PATH is set to ours (the agent's
// captured PATH, where tmux resolves), and TMUX is unset — a window opened
// from inside tmux must take go's attach path, not switch-client the
// caller's own client.
func openLinuxTerminal(app, shellCmd string) error {
	candidates := linuxTerminals
	if app != "" {
		candidates = nil
		for _, t := range linuxTerminals {
			if t.name == app {
				candidates = []linuxTerminal{t}
			}
		}
		if candidates == nil {
			return fmt.Errorf("unknown terminal app %q in %s (have: %s)", app, ConfigPath(), terminalNames(linuxTerminals))
		}
	}
	for _, t := range candidates {
		path, err := lookPath(t.name)
		if err != nil {
			continue
		}
		script := "unset TMUX TMUX_PANE; PATH=" + ShellQuote(os.Getenv("PATH")) + " " + shellCmd + " || read -r _"
		args := append(append([]string{}, t.prefix...), "sh", "-c", script)
		return startTerminal(path, args...)
	}
	return fmt.Errorf("no terminal found on PATH (tried: %s) — set [terminal] app in %s", terminalNames(candidates), ConfigPath())
}

func terminalNames(ts []linuxTerminal) string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.name
	}
	return strings.Join(names, ", ")
}
