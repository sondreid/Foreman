package main

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"fyne.io/systray"
)

// The menu bar icon is the foreman mark rendered black-on-transparent — a
// macOS template image, so the system recolors it for light/dark menu bars.
// Derived from docs/assets/logo/logo-mark.svg (regeneration recipe in the
// logo README).
//
//go:embed icon-template.png
var iconTemplate []byte

func setTrayIcon() {
	systray.SetTemplateIcon(iconTemplate, iconTemplate)
}

// confirmDone asks before teardown — "closing is harmless, ending is
// explicit": a menu misclick must never kill a participant (issue #72).
// The message travels as an osascript argument (#30 rule). Bounded like all
// automation (#62) but on a human scale: the dialog gives itself up after
// 60s (with a Go-side backstop), and anything but an explicit Done — cancel,
// give-up, timeout, error — answers no.
func confirmDone(project, role string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "osascript",
		"-e", "on run argv",
		"-e", `set r to display dialog (item 1 of argv) with title "foreman" buttons {"Cancel", "Done"} default button "Cancel" cancel button "Cancel" with icon caution giving up after 60`,
		"-e", `if gave up of r then error number -128`,
		"-e", "end run",
		fmt.Sprintf("End %s in project %s? Its terminal closes for real.", role, project)).Run()
	return err == nil
}

// promptText asks for a line of text in a dialog, prefilled with def; ok
// labels the confirming button. Same rules as confirmDone: the strings
// travel as osascript arguments (#30), the dialog gives itself up after 60s
// with a Go-side backstop (#62), and anything but an explicit ok — cancel,
// give-up, timeout, error, an emptied field — returns "" and means no.
func promptText(msg, def, ok string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript",
		"-e", "on run argv",
		"-e", `set r to display dialog (item 1 of argv) with title "foreman" default answer (item 2 of argv) buttons {"Cancel", item 3 of argv} default button 2 cancel button "Cancel" giving up after 60`,
		"-e", `if gave up of r then error number -128`,
		"-e", `return text returned of r`,
		"-e", "end run",
		msg, def, ok).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// notify posts a macOS notification; the message travels as an osascript
// argument, never interpolated into the script (same rule as issue #30).
func notify(msg string) {
	exec.Command("osascript",
		"-e", "on run argv",
		"-e", `display notification (item 1 of argv) with title "foreman"`,
		"-e", "end run", msg).Run()
}
