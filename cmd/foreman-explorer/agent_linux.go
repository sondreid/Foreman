package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const agentUnitName = "foreman-explorer.service"

// procNameMax is Linux's process name length (TASK_COMM_LEN minus the NUL),
// which pgrep -x matches against: "foreman-explorer" shows as
// "foreman-explore".
const procNameMax = 15

func agentUnitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", agentUnitName)
}

// agentUnit renders the systemd user unit, the counterpart of the macOS
// LaunchAgent: started with the graphical session, restarted only on a crash
// (Restart=on-failure), so a clean Quit from the menu stays quit.
// KillMode=process signals only the explorer on stop or restart — the
// default control-group mode would also kill a tmux server the explorer's
// fm new happened to start, and every terminal it opened. PATH is the
// installing shell's, so tmux resolves as in the user's terminals rather
// than from systemd's minimal default.
func agentUnit(exePath, path string) string {
	return fmt.Sprintf(`[Unit]
Description=foreman explorer
PartOf=graphical-session.target
After=graphical-session.target

[Service]
ExecStart=%s
Environment=%s
Restart=on-failure
RestartSec=3
KillMode=process

[Install]
WantedBy=graphical-session.target
`, unitQuote(exePath), unitQuote("PATH="+path))
}

// unitQuote makes s one systemd unit-file word: double-quoted, with quotes
// and backslashes escaped and % doubled so it isn't read as a specifier.
func unitQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`
}

// installAgent writes the unit for this very binary, enables it, stops any
// explorer already running, then restarts the unit. restart, not
// enable --now: the stop above is a clean exit Restart=on-failure won't
// undo, and enable --now is a no-op on a unit that was still active.
func installAgent() error {
	exePath, err := agentExecutable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	unitPath := agentUnitPath()
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(agentUnit(exePath, os.Getenv("PATH"))), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", agentUnitName); err != nil {
		return err
	}
	terminateOtherInstances(exePath)
	return systemctl("restart", agentUnitName)
}

// uninstallAgent stops and removes the unit; a missing unit is fine.
func uninstallAgent() error {
	systemctl("disable", "--now", agentUnitName)
	if err := os.Remove(agentUnitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
