package core

import (
	"errors"
	"strings"
	"testing"
)

var errFake = errors.New("fake tmux failure")

// recorders swap both seams and log every call; restored via t.Cleanup.
func recordCalls(t *testing.T, tmuxOut func(args []string) (string, bool), osaOut string) (tmuxCalls, osaCalls *[]string) {
	t.Helper()
	var tc, oc []string
	origT, origO := tmuxRun, runOsascript
	t.Cleanup(func() { tmuxRun, runOsascript = origT, origO })
	tmuxRun = func(args ...string) ([]byte, error) {
		tc = append(tc, strings.Join(args, " "))
		if out, ok := tmuxOut(args); ok {
			return []byte(out + "\n"), nil
		}
		return []byte("boom"), errFake
	}
	runOsascript = func(args ...string) (string, error) {
		oc = append(oc, strings.Join(args, " "))
		return osaOut, nil
	}
	return &tc, &oc
}

// trackingFake builds a tmux fake serving one option listing, one
// attached-clients output, and per-key value reads consistent with the
// listing (issue #60's verify-before-write reads entries fresh).
func trackingFake(listing, clients string) func(args []string) (string, bool) {
	values := map[string]string{}
	for _, line := range strings.Split(listing, "\n") {
		if name, v, ok := strings.Cut(line, " "); ok {
			values[name] = strings.Trim(v, `"`)
		}
	}
	return func(args []string) (string, bool) {
		switch {
		case args[0] == "show-options" && len(args) == 2: // the -s listing
			return listing, true
		case args[0] == "show-options": // per-key fresh read
			return values[args[len(args)-1]], true
		case args[0] == "list-clients":
			return clients, true
		}
		return "", true
	}
}

// fixGOOS pins the platform OpenTerminal dispatches on; restored via t.Cleanup.
func fixGOOS(t *testing.T, platform string) {
	t.Helper()
	orig := goos
	t.Cleanup(func() { goos = orig })
	goos = platform
}

// fixNow pins the sweep's clock; restored via t.Cleanup.
func fixNow(t *testing.T, ts int64) {
	t.Helper()
	orig := nowUnix
	t.Cleanup(func() { nowUnix = orig })
	nowUnix = func() int64 { return ts }
}

func TestSplitStrike(t *testing.T) {
	cases := []struct {
		value, tty string
		struck     bool
		since      int64
	}{
		{"ttys012", "ttys012", false, 0},
		{"ttys012?", "ttys012", true, 0}, // pre-#95 bare strike
		{"ttys012?900", "ttys012", true, 900},
	}
	for _, c := range cases {
		tty, struck, since := splitStrike(c.value)
		if tty != c.tty || struck != c.struck || since != c.since {
			t.Errorf("splitStrike(%q) = (%q, %v, %d), want (%q, %v, %d)",
				c.value, tty, struck, since, c.tty, c.struck, c.since)
		}
	}
}

func TestTermWinKey(t *testing.T) {
	if got := termWinKey("77"); got != "@fm_win_77" {
		t.Errorf("got %q", got)
	}
}

func TestOpenTerminalRecordsWindow(t *testing.T) {
	fixGOOS(t, "darwin")
	tmuxCalls, _ := recordCalls(t, trackingFake("", ""), "/dev/ttys042\n77")
	if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err != nil {
		t.Fatal(err)
	}
	want := "set-option -s @fm_win_77 ttys042"
	if got := (*tmuxCalls)[len(*tmuxCalls)-1]; got != want {
		t.Errorf("last tmux call = %q, want %q", got, want)
	}
}

func TestOpenTerminalPurgesRecycledTty(t *testing.T) {
	fixGOOS(t, "darwin")
	// Window 60's shell already exited; its pty was recycled to the window
	// being opened. The stale entry must be closed out, not left to shadow
	// the new one in the sweep (issue #56).
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake("@fm_win_60 ttys042", ""), "/dev/ttys042\n77")
	if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err != nil {
		t.Fatal(err)
	}
	if len(*osaCalls) != 2 || !strings.HasSuffix((*osaCalls)[1], " 60 /dev/ttys042") {
		t.Errorf("want the stale window 60 closed, got %v", *osaCalls)
	}
	joined := strings.Join(*tmuxCalls, ";")
	if !strings.Contains(joined, "set-option -s -u @fm_win_60") {
		t.Errorf("stale entry not forgotten: %s", joined)
	}
	if !strings.Contains(joined, "set-option -s @fm_win_77 ttys042") {
		t.Errorf("new window not recorded: %s", joined)
	}
}

func TestOpenTerminalRejectsMalformedReply(t *testing.T) {
	fixGOOS(t, "darwin")
	// A reply that doesn't parse means an untracked window — that's an
	// error, not a success (issue #61).
	for _, reply := range []string{"", "onlyoneline", "notatty\n77", "/dev/ttys042\nNaN"} {
		_, _ = recordCalls(t, trackingFake("", ""), reply)
		if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err == nil {
			t.Errorf("reply %q: want error, got nil", reply)
		}
	}
}

func TestOpenTerminalFailsWhenTrackingNotPersisted(t *testing.T) {
	fixGOOS(t, "darwin")
	fake := func(args []string) (string, bool) {
		if args[0] == "set-option" {
			return "", false // persistence fails
		}
		return trackingFake("", "")(args)
	}
	_, _ = recordCalls(t, fake, "/dev/ttys042\n77")
	if err := OpenTerminal("", "'fm' 'A' go 'coder'"); err == nil {
		t.Error("want error when tracking cannot be persisted")
	}
}

func TestOpenTerminalGhosttyIsUntracked(t *testing.T) {
	fixGOOS(t, "darwin")
	// Ghostty exposes no tty to match a tmux client against, so nothing is
	// recorded; the typed exit closes the window once fm returns.
	tmuxCalls, osaCalls := recordCalls(t, trackingFake("", ""), "")
	if err := OpenTerminal("ghostty", "'fm' 'A' go 'coder'"); err != nil {
		t.Fatal(err)
	}
	if len(*osaCalls) != 1 || !strings.Contains((*osaCalls)[0], `tell application "Ghostty"`) ||
		!strings.HasSuffix((*osaCalls)[0], " 'fm' 'A' go 'coder' && exit") {
		t.Errorf("osascript calls = %v", *osaCalls)
	}
	if len(*tmuxCalls) != 0 {
		t.Errorf("ghostty windows must not be tracked, got tmux calls %v", *tmuxCalls)
	}
}

func TestOpenTerminalRejectsUnknownApp(t *testing.T) {
	fixGOOS(t, "darwin")
	// A typo in [terminal] must not silently open Terminal.app instead.
	_, osaCalls := recordCalls(t, trackingFake("", ""), "")
	if err := OpenTerminal("iterm", "'fm' 'A' go 'coder'"); err == nil {
		t.Error("want error for unknown app")
	}
	if len(*osaCalls) != 0 {
		t.Errorf("unknown app must not run osascript, got %v", *osaCalls)
	}
}

func TestCloseTermWindow(t *testing.T) {
	tmuxCalls, osaCalls := recordCalls(t, trackingFake("@fm_win_77 ttys042", ""), "")
	CloseTermWindow("/dev/ttys042")
	if len(*osaCalls) != 1 || !strings.HasSuffix((*osaCalls)[0], " 77 /dev/ttys042") {
		t.Errorf("osascript calls = %v, want one close keyed by window id and tty", *osaCalls)
	}
	if !strings.Contains((*osaCalls)[0], "first tab of window id wid whose tty is") {
		t.Errorf("must close the tab inside the tracked window, never more: %v", *osaCalls)
	}
	if got := (*tmuxCalls)[len(*tmuxCalls)-1]; got != "set-option -s -u @fm_win_77" {
		t.Errorf("last tmux call = %q, want the unset", got)
	}
}

func TestCloseTermWindowUntrackedIsNoop(t *testing.T) {
	_, osaCalls := recordCalls(t, trackingFake("", ""), "")
	CloseTermWindow("/dev/ttys099") // listing empty: not tracked
	if len(*osaCalls) != 0 {
		t.Errorf("untracked tty must not touch Terminal, got %v", *osaCalls)
	}
}

func TestCloseResultProtocol(t *testing.T) {
	// The AppleScript's return word decides the entry's fate (issue #59):
	// busy keeps it (slow teardown may still be a husk — sweep re-judges);
	// every terminal outcome forgets it.
	cases := map[string]bool{ // result → entry must be forgotten
		"closed": true, "gone": true, "notab": true, "kepttab": true,
		"busy": false,
	}
	for result, wantForget := range cases {
		tmuxCalls, _ := recordCalls(t, trackingFake("@fm_win_77 ttys042", ""), result)
		CloseTermWindow("/dev/ttys042")
		gotForget := strings.Contains(strings.Join(*tmuxCalls, ";"), "-u @fm_win_77")
		if gotForget != wantForget {
			t.Errorf("result %q: forgotten=%v, want %v", result, gotForget, wantForget)
		}
	}
}

func TestCloseTermWindowFailureKeepsTracking(t *testing.T) {
	// osascript failing (Terminal unreachable, automation denied) must not
	// unset the entry — forgetting it would strand the husk forever; kept,
	// the sweep retries (issue #56).
	tmuxCalls, _ := recordCalls(t, trackingFake("@fm_win_77 ttys042", ""), "")
	runOsascript = func(args ...string) (string, error) { return "", errFake }
	CloseTermWindow("/dev/ttys042")
	if joined := strings.Join(*tmuxCalls, ";"); strings.Contains(joined, "-u @fm_win_77") {
		t.Errorf("failed close must keep the tracking entry: %s", joined)
	}
}

func TestSweepFirstAbsenceOnlyStrikes(t *testing.T) {
	fixNow(t, 1000)
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake("@fm_win_77 ttys012\nstatus-interval 15", ""), "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("first absence must not close, got %v", *osaCalls)
	}
	if !strings.Contains(strings.Join(*tmuxCalls, ";"), "set-option -s @fm_win_77 ttys012?1000") {
		t.Errorf("want a timestamped strike recorded, got %v", *tmuxCalls)
	}
}

func TestSweepAgedStrikeCloses(t *testing.T) {
	fixNow(t, 1000) // strike from t=900: absent a full strikeMinAge
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake(`@fm_win_77 "ttys012?900"`+"\n@fm_win_88 ttys999", "/dev/ttys999"), "")
	SweepTermWindows()
	if len(*osaCalls) != 1 || !strings.HasSuffix((*osaCalls)[0], " 77 /dev/ttys012") {
		t.Errorf("want exactly the struck orphan closed, got %v", *osaCalls)
	}
	joined := strings.Join(*tmuxCalls, ";")
	if !strings.Contains(joined, "set-option -s -u @fm_win_77") {
		t.Errorf("orphan not forgotten: %s", joined)
	}
	if strings.Contains(joined, "@fm_win_88 ttys999?") || strings.Contains(joined, "-u @fm_win_88") {
		t.Errorf("attached window must stay untouched: %s", joined)
	}
}

func TestSweepYoungStrikeWaits(t *testing.T) {
	// A newborn window struck moments ago (issue #95): back-to-back sweeps —
	// a click-triggered rebuild then the periodic tick — must not close it,
	// or rewrite the strike (which would reset its age forever).
	fixNow(t, 1000)
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake(`@fm_win_77 "ttys012?995"`, ""), "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("young strike must not close, got %v", *osaCalls)
	}
	if joined := strings.Join(*tmuxCalls, ";"); strings.Contains(joined, "set-option -s @fm_win_77") ||
		strings.Contains(joined, "-u @fm_win_77") {
		t.Errorf("young strike must be left untouched: %s", joined)
	}
}

func TestSweepLegacyBareStrikeRestamped(t *testing.T) {
	// A pre-#95 strike has no timestamp — age unknown, so it must be
	// re-stamped, never closed on it.
	fixNow(t, 1000)
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake(`@fm_win_77 "ttys012?"`, ""), "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("unknown-age strike must not close, got %v", *osaCalls)
	}
	if !strings.Contains(strings.Join(*tmuxCalls, ";"), "set-option -s @fm_win_77 ttys012?1000") {
		t.Errorf("want the strike re-stamped, got %v", *tmuxCalls)
	}
}

func TestSweepReattachClearsStrike(t *testing.T) {
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake(`@fm_win_77 "ttys012?990"`, "/dev/ttys012"), "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("reattached client must not be closed, got %v", *osaCalls)
	}
	if !strings.Contains(strings.Join(*tmuxCalls, ";"), "set-option -s @fm_win_77 ttys012") {
		t.Errorf("want the strike cleared, got %v", *tmuxCalls)
	}
}

func TestSweepAbortsWhenClientQueryFails(t *testing.T) {
	_, osaCalls := recordCalls(t, func(args []string) (string, bool) {
		if args[0] == "show-options" && len(args) == 2 {
			return "@fm_win_77 ttys012", true
		}
		if args[0] == "list-clients" {
			return "", false // query FAILS — must not read as "no clients"
		}
		return "", true
	}, "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("uncertain client state must abort the sweep, got %v", *osaCalls)
	}
}

func TestSweepSkipsEntryChangedSinceSnapshot(t *testing.T) {
	// Between the sweep's listing and its write, a concurrent record retired
	// the entry (fresh read disagrees) — the sweep must not strike/close it,
	// which would resurrect a stale option (issue #60).
	base := trackingFake("@fm_win_77 ttys012", "")
	fake := func(args []string) (string, bool) {
		if args[0] == "show-options" && len(args) > 2 {
			return "", true // fresh read: entry already gone
		}
		return base(args)
	}
	tmuxCalls, osaCalls := recordCalls(t, fake, "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("changed entry must not be acted on, got %v", *osaCalls)
	}
	if joined := strings.Join(*tmuxCalls, ";"); strings.Contains(joined, "@fm_win_77 ttys012?") {
		t.Errorf("stale strike written — resurrection: %s", joined)
	}
}

func TestSweepMigratesLegacyEntry(t *testing.T) {
	// Pre-#56 format: @fm_win_<tty> = <window id>. Rewritten id-keyed, not
	// closed — the next pass judges it with fresh strikes.
	tmuxCalls, osaCalls := recordCalls(t,
		trackingFake("@fm_win_ttys012 77", ""), "")
	SweepTermWindows()
	if len(*osaCalls) != 0 {
		t.Errorf("migration must not close anything, got %v", *osaCalls)
	}
	joined := strings.Join(*tmuxCalls, ";")
	if !strings.Contains(joined, "set-option -s -u @fm_win_ttys012") {
		t.Errorf("legacy entry not removed: %s", joined)
	}
	if !strings.Contains(joined, "set-option -s @fm_win_77 ttys012") {
		t.Errorf("legacy entry not rewritten id-keyed: %s", joined)
	}
}
