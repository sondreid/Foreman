package core

import (
	"fmt"
	"strings"
	"unicode"
)

// reservedProjectNames are the CLI's verbs and global commands — a project
// with one of these names would dispatch as the command instead of the
// project (or never be addressable with the name omitted). Must track the
// verb switch and projectVerbs map in cmd/foreman/main.go.
var reservedProjectNames = map[string]bool{
	"new": true, "go": true, "adopt": true, "done": true, "pin": true, "unpin": true,
	"rename": true, "restore": true, "list": true, "init": true, "help": true,
}

// ValidateProjectName rejects names foreman could never work with: tmux
// accepts ':' and '.' in a session name but its targets split on them, so
// such a session could never be addressed again; '[' and ']' would corrupt
// the config section header, control characters the line-based tmux output
// foreman parses; a leading '-' reads as a flag, "__" is the plumbing-verb
// prefix, and reserved names dispatch as commands.
func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is empty")
	}
	if reservedProjectNames[name] {
		return fmt.Errorf("%q is a foreman verb — reserved as a project name", name)
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "__") {
		return fmt.Errorf("project name %q won't work: cannot start with - or __", name)
	}
	if strings.ContainsAny(name, ".:[]") || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("project name %q won't work: no . : [ ] or control characters", name)
	}
	return nil
}
