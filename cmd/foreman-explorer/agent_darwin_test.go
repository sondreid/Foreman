package main

import (
	"strings"
	"testing"
)

func TestAgentPlist(t *testing.T) {
	p := agentPlist("/home/a&b/fm-explorer", "/home/a&b/.local/state/foreman/explorer.log")
	for _, want := range []string{
		agentLabel,
		"<string>/home/a&amp;b/fm-explorer</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
	// The raw, unescaped path must never appear — an & would make invalid XML.
	if strings.Contains(p, "a&b/fm-explorer") {
		t.Error("exe path was not XML-escaped")
	}
}
