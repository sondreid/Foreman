package main

import (
	"fmt"
	"os"
	"time"

	"github.com/arnehalvorsen/foreman/internal/core"
)

const usage = `foreman — organize terminals by project, on top of tmux

usage:
  foreman init                     write a starter config to edit
  foreman list                     all projects and their terminals
  foreman <project> new <role>     new terminal in the project (runs role preset, if any;
                                   a second one is numbered: coder-2)
  foreman <project> go <role>      move that terminal's view here (--mirror to view alongside,
                                   --window to open a new terminal window instead)
  foreman <project> list           terminals in the project
  foreman <project> adopt <role>   pull the current terminal into the project (outside
                                   tmux: new shell in the current dir, viewed here)
  foreman <project> done [role]    close one terminal, or the whole project
  foreman <project> pin|unpin      mark the project as priority
  foreman <project> rename <name>  give the project a new name (updates config too)
  foreman restore [project]        recreate the terminals a tmux server restart took
                                   down (power-down); the role preset runs again with
                                   the same $FM_SESSION, so a cmd written as
                                   "claude --resume $FM_SESSION || claude --session-id
                                   $FM_SESSION" resumes its conversation

<project> can be omitted (foreman new coder): inferred from the managed
terminal you're in, else the configured root containing the current
directory, else the directory's name. Verb names are reserved as
project names. Bare "foreman list" always shows all projects.

config: ~/.config/foreman/config.toml
  [role.planner]
  cmd = "claude --permission-mode plan"

  [project.A]
  root = "~/code/project-a"
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return
	}
	if args[0] == "list" {
		fail(cmdListAll())
		return
	}
	if args[0] == "init" {
		fail(cmdInit())
		return
	}
	// Like bare list: no project means every project.
	if args[0] == "restore" {
		project := ""
		if len(args) > 1 {
			project = args[1]
		}
		fail(cmdRestore(project))
		return
	}
	// Plumbing for the status-bar ticker (set up by StyleSession); not part
	// of the human-facing surface, so absent from usage.
	if args[0] == "__ticker" {
		if len(args) > 1 {
			fail(cmdTicker(args[1]))
		}
		return
	}
	// Plumbing for done's detached cleanup worker (issue #46): closes the
	// tracked windows of the given ttys after the kill has landed.
	if args[0] == "__closewins" {
		time.Sleep(500 * time.Millisecond)
		for _, tty := range args[1:] {
			core.CloseTermWindow(tty)
		}
		return
	}

	// Verb in first position → the project was omitted: infer it and
	// prepend, so the rest of the parse is unchanged.
	if projectVerbs[args[0]] {
		name, source, err := core.CurrentProject(core.LoadConfig())
		fail(err)
		fmt.Fprintf(os.Stderr, "foreman: project %s (from %s)\n", name, source)
		args = append([]string{name}, args...)
	}

	project := args[0]
	if len(args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	verb := args[1]
	arg := ""
	if len(args) > 2 {
		arg = args[2]
	}

	switch verb {
	case "new":
		requireArg(verb, arg, "role")
		fail(cmdNew(project, arg))
	case "go":
		requireArg(verb, arg, "role")
		if hasFlag(args[3:], "--window") {
			fail(cmdGoWindow(project, arg))
		} else {
			fail(cmdGo(project, arg, hasFlag(args[3:], "--mirror")))
		}
	case "list":
		fail(cmdList(project))
	case "adopt":
		requireArg(verb, arg, "role")
		fail(cmdAdopt(project, arg))
	case "done":
		fail(cmdDone(project, arg))
	case "pin":
		fail(cmdPin(project, true))
	case "unpin":
		fail(cmdPin(project, false))
	case "rename":
		requireArg(verb, arg, "name")
		fail(cmdRename(project, arg))
	case "restore":
		fail(cmdRestore(project))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", verb, usage)
		os.Exit(2)
	}
}

// projectVerbs are the project-scoped verbs; in first position they mean
// the project name was omitted. This reserves them as project names
// (list/init/help were already taken by the global commands). Must track
// reservedProjectNames in internal/core/name.go.
var projectVerbs = map[string]bool{
	"new": true, "go": true, "adopt": true, "done": true, "pin": true, "unpin": true,
	"rename": true,
}

func hasFlag(rest []string, flag string) bool {
	for _, a := range rest {
		if a == flag {
			return true
		}
	}
	return false
}

func requireArg(verb, arg, what string) {
	if arg == "" {
		fmt.Fprintf(os.Stderr, "usage: foreman <project> %s <%s>\n", verb, what)
		os.Exit(2)
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "foreman:", err)
		os.Exit(1)
	}
}
