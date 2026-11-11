package core

import (
	"regexp"
	"testing"
)

func TestNewSessionIDIsV4UUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if id := NewSessionID(); !re.MatchString(id) {
		t.Errorf("not a v4 uuid: %s", id)
	}
	if NewSessionID() == NewSessionID() {
		t.Error("ids repeat")
	}
}

func TestJournalRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ps, err := Participants()
	if err != nil || len(ps) != 0 {
		t.Fatalf("empty journal: %v, %v", ps, err)
	}
	a := Participant{ID: "a", Project: "A", Name: "coder", Dir: "/tmp/a", Created: 20}
	b := Participant{ID: "b", Project: "A", Name: "coder-2", Dir: "/tmp/a", Created: 10}
	c := Participant{ID: "c", Project: "B", Name: "coder", Dir: "/tmp/b", Created: 30}
	for _, p := range []Participant{a, b, c} {
		if err := RecordParticipant(p); err != nil {
			t.Fatal(err)
		}
	}
	ps, _ = Participants()
	if len(ps) != 3 || ps[0] != b || ps[1] != a || ps[2] != c {
		t.Fatalf("want oldest first [b a c], got %+v", ps)
	}
	if err := RenameParticipants("A", "Z"); err != nil {
		t.Fatal(err)
	}
	ForgetParticipants("Z", "coder-2")
	ps, _ = Participants()
	if len(ps) != 2 || ps[0].Project != "Z" || ps[0].Name != "coder" || ps[1] != c {
		t.Fatalf("after rename+forget: %+v", ps)
	}
	ForgetParticipants("Z", "")
	if ps, _ = Participants(); len(ps) != 1 || ps[0] != c {
		t.Fatalf("after forget all: %+v", ps)
	}
}

func TestPlanRestore(t *testing.T) {
	old := Participant{ID: "old", Name: "coder", Created: 50}
	oldGone := Participant{ID: "old-gone", Name: "planner", Created: 60}
	fresh := Participant{ID: "fresh", Name: "coder", Created: 150}
	freshGone := Participant{ID: "fresh-gone", Name: "shell", Created: 160}
	entries := []Participant{old, oldGone, fresh, freshGone}
	live := map[string]bool{"coder": true}

	restore, stale := PlanRestore(entries, live, 100)
	if len(restore) != 1 || restore[0].ID != "old-gone" {
		t.Errorf("restore = %+v, want only old-gone", restore)
	}
	if len(stale) != 2 || stale[0].ID != "old" || stale[1].ID != "fresh-gone" {
		t.Errorf("stale = %+v, want old (name retaken) and fresh-gone (closed on purpose)", stale)
	}

	restore, stale = PlanRestore(entries, nil, 0)
	if len(restore) != 3 || restore[0].ID != "fresh" || len(stale) != 1 || stale[0].ID != "old" {
		t.Errorf("no server: want newest coder + planner + shell restored and old coder dropped, got restore=%+v stale=%+v", restore, stale)
	}
}
