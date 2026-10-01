package main

import (
	"strings"
	"testing"
)

func TestAgentUnit(t *testing.T) {
	u := agentUnit(`/home/a b/100%/fm-explorer`, "/usr/bin:/home/a/.local/bin")
	for _, want := range []string{
		`ExecStart="/home/a b/100%%/fm-explorer"`,
		`Environment="PATH=/usr/bin:/home/a/.local/bin"`,
		"Restart=on-failure",
		"KillMode=process",
		"WantedBy=graphical-session.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q:\n%s", want, u)
		}
	}
}
