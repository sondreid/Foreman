package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The explorer can run as a login agent — a macOS LaunchAgent or a systemd
// user unit on Linux — so it starts at login and, crucially, survives its
// launch terminal: a plain `fm-explorer &` is a child of that terminal and
// dies when it closes (issue #80). Opt-in via `fm-explorer install-agent`;
// the menu's Quit still quits (see agentPlist, agentUnit).

// stateDir holds the explorer's log — XDG-ish, matching foreman's own
// ~/.config and ~/.local/bin choices rather than ~/Library.
func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "foreman")
}

func logPath() string { return filepath.Join(stateDir(), "explorer.log") }

// agentExecutable is this binary's path, symlinks resolved — what the agent
// starts.
func agentExecutable() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return exePath, nil
}

// terminateOtherInstances stops every running explorer except this
// short-lived install-agent process. pgrep -x matches the kernel's process
// name, which is truncated to procNameMax. Best-effort: no pgrep, no
// matches, or a kill that races an already-exiting process are all fine.
func terminateOtherInstances(exePath string) {
	self := os.Getpid()
	name := filepath.Base(exePath)
	if len(name) > procNameMax {
		name = name[:procNameMax]
	}
	out, err := exec.Command("pgrep", "-u", strconv.Itoa(os.Getuid()), "-x", name).Output()
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid == self {
			continue
		}
		exec.Command("kill", field).Run()
	}
}
