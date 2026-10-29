package main

import (
	"testing"

	"github.com/arnehalvorsen/foreman/internal/core"
)

// Two structurally different menus must never produce the same fingerprint,
// even when a role name contains the bytes an in-band delimiter scheme would
// have used (issue #79 review). Here project "a" with role "b=c" vs project
// "a=b" with role "c" would collide under a "=" delimiter.
func TestMenuShapeNoDelimiterCollision(t *testing.T) {
	one := []core.Project{{Name: "a", Windows: []core.Window{{ID: "@1", Role: "b=c"}}}}
	two := []core.Project{{Name: "a=b", Windows: []core.Window{{ID: "@1", Role: "c"}}}}
	if menuShape(one, nil, nil) == menuShape(two, nil, nil) {
		t.Error("distinct menu structures collided to one fingerprint")
	}
}

// A membership change (a new participant) must change the fingerprint, while
// a pure label change (window id/role unchanged) must not — that split is
// what lets refresh() retitle in place instead of rebuilding.
func TestMenuShapeMembershipVsLabel(t *testing.T) {
	base := []core.Project{{Name: "p", Windows: []core.Window{{ID: "@1", Role: "coder"}}}}
	added := []core.Project{{Name: "p", Windows: []core.Window{{ID: "@1", Role: "coder"}, {ID: "@2", Role: "shell"}}}}
	if menuShape(base, nil, nil) == menuShape(added, nil, nil) {
		t.Error("adding a participant did not change the fingerprint")
	}
	if menuShape(base, nil, nil) != menuShape(base, nil, nil) {
		t.Error("same structure produced different fingerprints")
	}
}

func TestCreatedNameReadsNewOutput(t *testing.T) {
	out := "foreman: project A (from terminal)\ncreated A/coder-2 in /Users/me/code a\n  foreman A go coder-2\n"
	if got := createdName(out, "A", "coder"); got != "coder-2" {
		t.Errorf("got %q, want coder-2", got)
	}
	if got := createdName("nothing useful", "A", "coder"); got != "coder" {
		t.Errorf("fallback: got %q, want coder", got)
	}
}
