package core

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// nowUnix is the sweep's clock; a seam so tests can age strikes at will.
var nowUnix = func() int64 { return time.Now().Unix() }

// strikeMinAge is how long a strike must stand before its window may be
// closed. Strikes used to be judged by sweep *count* alone, but sweeps are
// not evenly spaced — a click-triggered rebuild sweeps immediately, and the
// periodic tick can land right behind it, collapsing the newborn grace to
// milliseconds and closing a window whose client simply hadn't attached yet
// (issue #95). One tick cycle of wall-clock age restores the intended
// "absent across two looks a cycle apart" meaning.
const strikeMinAge = 10 * time.Second

// splitStrike parses a tracking value: the tty base, whether it carries a
// strike, and the strike's unix time (0 for a pre-#95 bare "?" strike).
func splitStrike(value string) (tty string, struck bool, since int64) {
	tty, rest, struck := strings.Cut(value, "?")
	since, _ = strconv.ParseInt(rest, 10, 64)
	return tty, struck, since
}

// strikeValue stamps a strike on a tty value: "<tty>?<unix time>".
func strikeValue(tty string) string {
	return fmt.Sprintf("%s?%d", tty, nowUnix())
}

// macOS Terminal.app integration: foreman opens windows for views, tracks
// them, and closes them again once their view moves away (issue #43) — the
// fix for "soulless" [detached] windows piling up. Tracking lives in tmux
// server options (@fm_win_<window id> = tty), keyed by Terminal's window id
// because ids are unique and never reused, while ttys are recycled the
// moment a shell exits and dead tabs keep reporting their stale tty
// (issue #56). The tty value is what matches a tracked window to its tmux
// client. Only windows foreman opened are tracked, so user-opened windows
// are never touched.

// runOsascript executes osascript; a seam so tests can fake it, like
// tmuxRun. Bounded: a hung Terminal.app or stuck AppleEvent must not block
// go, done's worker, or the explorer rebuild forever (issue #62) — the cap
// comfortably clears closeTracked's internal ~2s busy grace. A timeout is
// an error, so tracking entries are preserved for the sweep to retry.
var runOsascript = func(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("osascript timed out: %w", ctx.Err())
	}
	return strings.TrimSpace(string(out)), err
}

// goos is the platform OpenTerminal opens windows for; a seam so the macOS
// and Linux openers are both tested on any CI runner.
var goos = runtime.GOOS

// OpenTerminal opens a window running shellCmd in the configured terminal
// app. On macOS: "terminal" (Terminal.app, also when unset) or "ghostty"; on
// Linux see linuxTerminals. An unknown app is an error rather than a silent
// fallback.
func OpenTerminal(app, shellCmd string) error {
	if goos != "darwin" {
		return openLinuxTerminal(app, shellCmd)
	}
	switch app {
	case "", "terminal":
		return openTermWindow(shellCmd)
	case "ghostty":
		return openGhostty(shellCmd)
	}
	return fmt.Errorf("unknown terminal app %q in %s (have: terminal, ghostty)", app, ConfigPath())
}

// openTermWindow opens a Terminal.app window running shellCmd and records the
// window against its tty. The command travels as an osascript argument,
// never interpolated into the script (issue #30). An untracked window is a
// husk-to-be that cleanup can never find, so a reply that doesn't parse or
// tracking that doesn't persist is an error, not a success (issue #61).
func openTermWindow(shellCmd string) error {
	// Returns "<tty>\n<window id>" for tracking.
	out, err := runOsascript(
		"-e", "on run argv",
		"-e", `tell application "Terminal"`,
		"-e", "activate",
		"-e", "set t to do script (item 1 of argv)",
		"-e", "set w to (first window whose selected tab is t)",
		"-e", "set out to (tty of t) & linefeed & (id of w)",
		"-e", "end tell",
		"-e", "return out",
		"-e", "end run", shellCmd)
	if err != nil {
		return err
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return fmt.Errorf("unexpected Terminal reply %q — window opened but not tracked", out)
	}
	tty, id := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if !strings.HasPrefix(tty, "/dev/") {
		return fmt.Errorf("unexpected tty %q — window opened but not tracked", tty)
	}
	if _, err := strconv.Atoi(id); err != nil {
		return fmt.Errorf("unexpected window id %q — window opened but not tracked", id)
	}
	return recordTermWindow(tty, id)
}

// termWinKey maps a Terminal window id to its tracking option name.
func termWinKey(windowID string) string {
	return "@fm_win_" + windowID
}

// trackedWindows reads every tracking option: window id → tty. A trailing
// "?<unix time>" on the tty is the sweep's strike marker (when it was
// struck), kept verbatim. Nil means the listing itself failed — callers must
// treat that as "unknown", not "none".
func trackedWindows() map[string]string {
	out, err := Tmux("show-options", "-s")
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(name, "@fm_win_") {
			continue
		}
		m[strings.TrimPrefix(name, "@fm_win_")] = strings.Trim(value, `"`)
	}
	return m
}

// trackedValue re-reads one entry fresh. Mutations verify against this
// right before writing: record (a CLI process) and the sweep (the explorer)
// race over the same options with no cross-process lock, and acting on a
// stale snapshot can resurrect an entry the other side just retired
// (issue #60). The residual window shrinks from seconds to milliseconds.
func trackedValue(windowID string) string {
	out, err := Tmux("show-options", "-s", "-q", "-v", termWinKey(windowID))
	if err != nil {
		return ""
	}
	return strings.Trim(out, `"`)
}

func recordTermWindow(tty, windowID string) error {
	if tty == "" || windowID == "" {
		return fmt.Errorf("empty tty or window id")
	}
	base := filepath.Base(tty)
	// The new window owns this tty now, so any other entry still claiming it
	// is a husk whose shell already exited (macOS recycles ptys instantly).
	// Close it here — left tracked, the recycled tty's live client would make
	// the sweep believe the husk is attached, forever (issue #56).
	for id, val := range trackedWindows() {
		if tty, _, _ := splitStrike(val); id == windowID || tty != base {
			continue
		}
		// A concurrent sweep may have judged this entry since the snapshot.
		if tty, _, _ := splitStrike(trackedValue(id)); tty != base {
			continue
		}
		closeTracked(id, base)
	}
	if _, err := Tmux("set-option", "-s", termWinKey(windowID), base); err != nil {
		return fmt.Errorf("window opened but tracking not persisted: %w", err)
	}
	return nil
}

// CloseTermWindow closes the tracked Terminal view of one client tty, if
// any, and forgets it. Unknown ttys are a no-op — that's a user's own
// window. Only the foreman *tab* is closed — the user may have added tabs
// of their own (issue #47) — and only inside its recorded window, so a
// recycled tty can never match a tab in a window foreman didn't open
// (issue #56); Terminal closes the window itself when the last tab goes.
func CloseTermWindow(tty string) {
	base := filepath.Base(tty)
	for id, val := range trackedWindows() {
		if t, _, _ := splitStrike(val); t == base {
			closeTracked(id, base)
		}
	}
}

// closeTracked closes the tty's tab inside one tracked window and forgets
// the tracking when there is nothing left to manage. The script's return
// value is a protocol, and each word decides the entry's fate (issue #59):
//
//   - "closed", "gone", "notab" — done, forget the entry.
//   - "kepttab" — the user added tabs and this Terminal can't close a tab
//     object; the tab is left to the user rather than retried forever.
//     Forgotten, explicitly.
//   - "busy" — the tab still runs something after the ~2s grace. That's
//     either a tmux teardown slower than the grace (still a husk!) or a
//     user's own process — we can't tell yet, so the entry is KEPT and the
//     sweep re-judges it next cycle instead of stranding a husk.
//
// A busy tab is waited out for ~2s first — right after detach-client the
// tmux process is still exiting. When our tab is the window's only one —
// the normal case, foreman opens fresh windows — the *window* is closed:
// some Terminal builds reject `close` on tab objects with -1708, which is
// how husks piled up silently before (issue #56). A failed osascript run
// (Terminal unreachable, automation denied, timeout) also keeps the entry
// for retry.
func closeTracked(windowID, ttyBase string) {
	out, err := runOsascript(
		"-e", "on run argv",
		"-e", `tell application "Terminal"`,
		"-e", "set wid to (item 1 of argv) as integer",
		"-e", `if not (exists window id wid) then return "gone"`,
		"-e", "try",
		"-e", "set theTab to (first tab of window id wid whose tty is (item 2 of argv))",
		"-e", "on error",
		"-e", `return "notab"`,
		"-e", "end try",
		"-e", "repeat 20 times",
		"-e", "if not (busy of theTab) then exit repeat",
		"-e", "delay 0.1",
		"-e", "end repeat",
		"-e", `if busy of theTab then return "busy"`,
		"-e", "if (count of tabs of window id wid) is 1 then",
		"-e", "close window id wid",
		"-e", `return "closed"`,
		"-e", "end if",
		"-e", "try",
		"-e", "close theTab",
		"-e", "on error",
		"-e", `return "kepttab"`,
		"-e", "end try",
		"-e", "end tell",
		"-e", `return "closed"`,
		"-e", "end run", windowID, "/dev/"+ttyBase)
	if err != nil || out == "busy" {
		return // keep the entry: the sweep retries / re-judges
	}
	Tmux("set-option", "-s", "-u", termWinKey(windowID))
}

// SweepTermWindows closes tracked windows whose tmux client no longer exists
// — views that ended without passing through go/done (crashes, manual
// detach). Called from the explorer's poll. Two safety rules (issue #48):
// a failed client query aborts the sweep (uncertainty must not read as "no
// clients" and reap everything), and a window is only closed once it has
// been absent across a timestamped "?" strike at least strikeMinAge old —
// wall-clock age, not sweep count, because sweeps bunch up (a click-triggered
// rebuild right before the periodic tick) and back-to-back sweeps must not
// reap a newborn window whose client simply hasn't attached yet (issue #95).
func SweepTermWindows() {
	tracked := trackedWindows()
	if len(tracked) == 0 {
		return
	}
	clients, err := Tmux("list-clients", "-F", "#{client_name}")
	if err != nil {
		return
	}
	attached := map[string]bool{}
	for _, c := range strings.Split(clients, "\n") {
		if c != "" {
			attached[filepath.Base(c)] = true
		}
	}
	for id, value := range tracked {
		tty, struck, since := splitStrike(value)
		if _, err := strconv.Atoi(id); err != nil {
			// Pre-#56 entry, keyed the other way around (@fm_win_<tty> =
			// window id): rewrite in the id-keyed form (strike dropped —
			// costs one extra sweep cycle) and let the next pass judge it.
			Tmux("set-option", "-s", "-u", termWinKey(id))
			if _, err := strconv.Atoi(tty); err == nil {
				Tmux("set-option", "-s", termWinKey(tty), id)
			}
			continue
		}
		// Act only if the entry still reads as snapshotted — a concurrent
		// record may have retired it since; writing anyway would resurrect
		// a stale option whose recycled tty reads as attached (issue #60).
		if trackedValue(id) != value {
			continue
		}
		switch {
		case attached[tty]:
			if struck { // back: clear the strike
				Tmux("set-option", "-s", termWinKey(id), tty)
			}
		case struck && since == 0:
			// Pre-#95 bare "?" strike, age unknown: re-stamp instead of
			// closing — costs one strikeMinAge wait, same philosophy as the
			// legacy-entry migration above.
			Tmux("set-option", "-s", termWinKey(id), strikeValue(tty))
		case struck && nowUnix()-since >= int64(strikeMinAge/time.Second):
			// Absent the whole strike age: really gone.
			closeTracked(id, tty)
		case struck: // young strike: keep waiting, however often we sweep
		default: // first absence: strike, close once the strike has aged
			Tmux("set-option", "-s", termWinKey(id), strikeValue(tty))
		}
	}
}

// openGhostty types shellCmd into a new Ghostty window's default shell,
// followed by exit, so the window closes itself once the view moves away and
// stays open showing the error if fm fails. Ghostty's own `command` property
// can't be used: it forces wait-after-command, leaving every window behind.
// Nothing is tracked — Ghostty exposes no tty to match against tmux clients.
func openGhostty(shellCmd string) error {
	_, err := runOsascript(
		"-e", "on run argv",
		"-e", `tell application "Ghostty"`,
		"-e", "activate",
		"-e", "set cfg to new surface configuration",
		"-e", "set initial input of cfg to (item 1 of argv) & linefeed",
		"-e", "new window with configuration cfg",
		"-e", "end tell",
		"-e", "end run", shellCmd+" && exit")
	return err
}
