package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeZenity answers every dialog with out/err and records each call's
// arguments; restored via t.Cleanup.
func fakeZenity(t *testing.T, out string, err error) *[][]string {
	t.Helper()
	var calls [][]string
	orig := runZenity
	t.Cleanup(func() { runZenity = orig })
	runZenity = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(out), err
	}
	return &calls
}

func TestConfirmDoneOnlyExplicitYes(t *testing.T) {
	calls := fakeZenity(t, "", nil)
	if !confirmDone("A", "coder") {
		t.Error("exit 0 must confirm")
	}
	args := strings.Join((*calls)[0], "\n")
	for _, want := range []string{"--question", "--text=End coder in project A? Its terminal closes for real.", "--default-cancel", "--timeout=60"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %q", want, (*calls)[0])
		}
	}
	// Cancel (1), timeout (5), or zenity missing all answer no.
	_ = fakeZenity(t, "", errors.New("exit status 5"))
	if confirmDone("A", "coder") {
		t.Error("a failed dialog must not confirm")
	}
}

func TestPromptText(t *testing.T) {
	calls := fakeZenity(t, "new-name\n", nil)
	if got := promptText("Rename project A to:", "A", "Rename"); got != "new-name" {
		t.Errorf("got %q", got)
	}
	if args := strings.Join((*calls)[0], " "); !strings.Contains(args, "--entry-text=A") || !strings.Contains(args, "--ok-label=Rename") {
		t.Errorf("args = %q", (*calls)[0])
	}
	_ = fakeZenity(t, "typed\n", errors.New("exit status 1"))
	if got := promptText("Rename project A to:", "A", "Rename"); got != "" {
		t.Errorf("cancel must return empty, got %q", got)
	}
}
