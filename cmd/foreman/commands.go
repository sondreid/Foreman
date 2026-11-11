package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/arnehalvorsen/foreman/internal/core"
)

func cmdInit() error {
	path, err := core.InitConfig()
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s\nEdit it to define your roles and project roots.\n", path)
	return nil
}

func cmdNew(project, role string) error {
	// Validate before this can mint a session — a name tmux can't target
	// (or that dispatches as a verb) must never come to life (issue #73).
	if err := core.ValidateProjectName(project); err != nil {
		return err
	}
	cfg := core.LoadConfig()
	name := role
	if core.SessionExists(project) {
		ws, err := core.ListWindows(project)
		if err != nil {
			return err
		}
		name = core.FreeRoleName(ws, cfg, role)
	}
	dir := cfg.WorkDir(project, role)
	if err := createParticipant(cfg, project, name, dir, core.NewSessionID()); err != nil {
		return err
	}
	fmt.Printf("created %s/%s in %s\n", project, name, dir)
	// Only unknown roles get the notice — a role deliberately configured
	// without a cmd (a named plain shell) already explained itself.
	if _, known := cfg.Role(role); !known {
		fmt.Printf("note: role %q has no cmd in %s — opened a plain shell\n", role, core.ConfigPath())
	}
	// The typical next step is go, so offer it. Only on a TTY — scripted
	// use creates without going, as before.
	if stdinIsTTY() {
		fmt.Printf("go to %s? [Y/n] ", name)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			// EOF or a broken stdin is not an answer — never go on it.
			fmt.Println()
			return nil
		}
		if ans := strings.ToLower(strings.TrimSpace(line)); ans == "" || ans == "y" || ans == "yes" {
			return cmdGo(project, name, false)
		}
		return nil
	}
	if !core.InsideTmux() {
		fmt.Printf("  foreman %s go %s   # to jump there\n", project, name)
	}
	return nil
}

// createParticipant is the window-making half of new, shared with restore:
// the window (minting the session when it is the project's first), its
// role tag, the preset typed into its shell, and the journal entry restore
// will replay. id reaches the shell as FM_SESSION so the preset can resume
// by it (claude --resume $FM_SESSION || claude --session-id $FM_SESSION).
func createParticipant(cfg core.Config, project, name, dir, id string) error {
	env := "FM_SESSION=" + id
	var wid string
	var err error
	if !core.SessionExists(project) {
		wid, err = core.Tmux("new-session", "-d", "-s", core.SessionName(project), "-n", name,
			"-c", dir, "-e", env, "-P", "-F", "#{window_id}")
	} else {
		// -d: create in the background. Without it, new from inside an
		// attached terminal jumps the view before the go prompt below has
		// asked (issue #54) — moving the view is go's job alone.
		wid, err = core.Tmux("new-window", "-d", "-t", "="+core.SessionName(project), "-n", name,
			"-c", dir, "-e", env, "-P", "-F", "#{window_id}")
	}
	if err != nil {
		return err
	}
	core.Tmux("set-option", "-w", "-t", wid, "@fm_role", name)
	core.Tmux("set-option", "-w", "-t", wid, "automatic-rename", "off")
	core.StyleSession(project)

	// Role commands run inside the shell (not as the window command) so the
	// window survives the process exiting.
	if preset, _ := cfg.Role(name); preset.Cmd != "" {
		if _, err := core.Tmux("send-keys", "-t", wid, "-l", preset.Cmd); err != nil {
			return err
		}
		if _, err := core.Tmux("send-keys", "-t", wid, "Enter"); err != nil {
			return err
		}
	}
	p := core.Participant{ID: id, Project: project, Name: name, Dir: dir, Created: time.Now().Unix()}
	if err := core.RecordParticipant(p); err != nil {
		fmt.Fprintf(os.Stderr, "foreman: %s/%s will not survive a tmux server restart: %v\n", project, name, err)
	}
	return nil
}

// cmdRestore brings back the participants the tmux server took down with
// it — after a power-down, every journaled participant; otherwise only those
// older than the running server (issue: resume across reboots). Entries
// whose window was closed on purpose are forgotten instead. Never prompts,
// so a whole project comes back in one go.
func cmdRestore(project string) error {
	all, err := core.Participants()
	if err != nil {
		return err
	}
	cfg := core.LoadConfig()
	serverStart := core.ServerStart()
	byProject := map[string][]core.Participant{}
	var order []string
	for _, p := range all {
		if project != "" && p.Project != project {
			continue
		}
		if _, seen := byProject[p.Project]; !seen {
			order = append(order, p.Project)
		}
		byProject[p.Project] = append(byProject[p.Project], p)
	}
	restored := 0
	for _, name := range order {
		live := map[string]bool{}
		if core.SessionExists(name) {
			ws, _ := core.ListWindows(name)
			for _, w := range ws {
				live[w.RoleName()] = true
			}
		}
		restore, stale := core.PlanRestore(byProject[name], live, serverStart)
		for _, p := range stale {
			core.ForgetParticipant(p.ID)
			if p.Survived(serverStart) {
				fmt.Printf("%s/%s: dropped an older record (FM_SESSION was %s)\n", name, p.Name, p.ID)
			}
		}
		for _, p := range restore {
			if _, err := os.Stat(p.Dir); err != nil {
				fmt.Printf("%s/%s: directory %s is gone — not restored\n", name, p.Name, p.Dir)
				continue
			}
			if err := createParticipant(cfg, name, p.Name, p.Dir, p.ID); err != nil {
				fmt.Printf("%s/%s: %v\n", name, p.Name, err)
				continue
			}
			fmt.Printf("restored %s/%s in %s\n", name, p.Name, p.Dir)
			restored++
		}
	}
	if restored == 0 {
		fmt.Println("nothing to restore")
	}
	return nil
}

func stdinIsTTY() bool {
	// A real terminal check — /dev/null is a character device too, so the
	// ModeCharDevice heuristic misfires on scripted use (issue #28).
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// cmdGo moves the project's view to the invoking terminal. Any other client
// showing the session is detached — one project shows in one place, so going
// somewhere never multiplies terminals (pass mirror to view alongside).
func cmdGo(project, role string, mirror bool) error {
	w, err := core.FindWindow(project, role)
	if err != nil {
		return err
	}
	// Re-stamp styling on the most-used verb so sessions self-heal — e.g.
	// the ticker's baked binary path after a moved install (issue #37).
	core.StyleSession(project)
	if core.InsideTmux() {
		if _, err := core.Tmux("switch-client", "-t", w.ID); err != nil {
			return err
		}
		if !mirror {
			core.DetachOtherClients(project)
		}
		return nil
	}
	if _, err := core.Tmux("select-window", "-t", w.ID); err != nil {
		return err
	}
	if mirror {
		return core.TmuxExec("attach-session", "-t", "="+core.SessionName(project))
	}
	// Detach current viewers ourselves (closing their foreman-opened
	// windows) before exec replaces this process; -d stays as a stragglers
	// net for clients attaching in the gap.
	core.DetachViewers(project)
	return core.TmuxExec("attach-session", "-d", "-t", "="+core.SessionName(project))
}

// cmdGoWindow opens the participant's view in a new window of the configured
// terminal app instead of switching in place (issue #6). The go running
// inside the new window does the actual move; the window is closed again
// once its view later moves away (issue #43).
func cmdGoWindow(project, role string) error {
	if _, err := core.FindWindow(project, role); err != nil {
		return err
	}
	bin := core.BinaryPath()
	if bin == "" {
		return fmt.Errorf("cannot locate the foreman binary")
	}
	return core.OpenTerminal(core.LoadConfig().Terminal, fmt.Sprintf("%s %s go %s",
		core.ShellQuote(bin), core.ShellQuote(project), core.ShellQuote(role)))
}

func cmdAdopt(project, role string) error {
	// Same reason as new: adopt can mint the session (issue #73).
	if err := core.ValidateProjectName(project); err != nil {
		return err
	}
	if !core.InsideTmux() {
		return adoptPlainTerminal(project, role)
	}
	wid, err := core.Tmux("display-message", "-p", "#{window_id}")
	if err != nil {
		return err
	}
	// Same guard as new: a role is an identity — two windows with the same
	// @fm_role would make go/done resolve arbitrarily.
	if core.SessionExists(project) {
		if w, ferr := core.FindWindow(project, role); ferr == nil {
			if w.ID == wid {
				return fmt.Errorf("this terminal is already %s/%s", project, role)
			}
			return fmt.Errorf("project %s already has a %q terminal (%s) — pick another role name",
				project, role, w.ID)
		}
	}
	if !core.SessionExists(project) {
		if _, err := core.Tmux("new-session", "-d", "-s", core.SessionName(project), "-n", "__fm_placeholder"); err != nil {
			return err
		}
		defer func() {
			if w, err := core.FindWindow(project, "__fm_placeholder"); err == nil {
				core.Tmux("kill-window", "-t", w.ID)
			}
		}()
	}
	// move-window needs an explicit free index.
	ws, err := core.ListWindows(project)
	if err != nil {
		return err
	}
	next := 0
	for _, w := range ws {
		if out, err := core.Tmux("display-message", "-p", "-t", w.ID, "#{window_index}"); err == nil {
			if i, err := strconv.Atoi(out); err == nil && i >= next {
				next = i + 1
			}
		}
	}
	if _, err := core.Tmux("move-window", "-s", wid, "-t", fmt.Sprintf("=%s:%d", core.SessionName(project), next)); err != nil {
		return err
	}
	core.Tmux("set-option", "-w", "-t", wid, "@fm_role", role)
	core.Tmux("set-option", "-w", "-t", wid, "automatic-rename", "off")
	core.Tmux("rename-window", "-t", wid, role)
	core.StyleSession(project)
	// Follow the terminal to its new home so the user's view doesn't jump elsewhere.
	core.Tmux("switch-client", "-t", wid)
	fmt.Printf("adopted this terminal into %s as %q\n", project, role)
	return nil
}

// adoptPlainTerminal is adopt's outside-tmux path. A shell born in a plain
// terminal can never be moved into tmux — the OS offers no way to re-wire a
// live process's pty (issue #92) — so do the closest honest thing: create a
// participant whose shell starts where the user is, and attach this window
// as its viewer. The original shell resumes on detach; anything already
// running in it stays outside tmux, which the output says out loud.
func adoptPlainTerminal(project, role string) error {
	// Same identity guard as the tmux path, minus the "already adopted"
	// case — a plain terminal isn't a participant yet by definition.
	if core.SessionExists(project) {
		if w, err := core.FindWindow(project, role); err == nil {
			return fmt.Errorf("project %s already has a %q terminal (%s) — pick another role name",
				project, role, w.ID)
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	// Adopt means "continue here": the cwd wins over the configured
	// dir/root, and no role preset runs — adopt takes a shell as it is
	// (the tmux-window path doesn't run presets either).
	var wid string
	if !core.SessionExists(project) {
		wid, err = core.Tmux("new-session", "-d", "-s", core.SessionName(project), "-n", role,
			"-c", dir, "-P", "-F", "#{window_id}")
	} else {
		wid, err = core.Tmux("new-window", "-d", "-t", "="+core.SessionName(project), "-n", role,
			"-c", dir, "-P", "-F", "#{window_id}")
	}
	if err != nil {
		return err
	}
	core.Tmux("set-option", "-w", "-t", wid, "@fm_role", role)
	core.Tmux("set-option", "-w", "-t", wid, "automatic-rename", "off")
	fmt.Printf("a running shell can't move into tmux — created %s/%s in %s instead\n",
		project, role, dir)
	if r, _ := core.LoadConfig().Role(role); r.Cmd != "" {
		fmt.Printf("note: role %q has a cmd, which adopt does not run: %s\n", role, r.Cmd)
	}
	// Attaching needs a real terminal — scripted use creates without going,
	// same contract as new (issue #28), instead of dying half-done on the
	// attach after the participant already exists.
	if !stdinIsTTY() {
		fmt.Printf("  foreman %s go %s   # to view it here\n", project, role)
		return nil
	}
	fmt.Println("attaching this window to it (detach returns you to the shell you were in)")
	// cmdGo re-stamps styling and attaches; this window is user-opened, so
	// it is deliberately never recorded for auto-close.
	return cmdGo(project, role, false)
}

func cmdDone(project, role string) error {
	if role != "" {
		w, err := core.FindWindow(project, role)
		if err != nil {
			return err
		}
		// Forget first: run from inside the window, this process dies with
		// the kill, and a surviving entry would come back on restore.
		core.ForgetParticipants(project, w.RoleName())
		if _, err := core.Tmux("kill-window", "-t", w.ID); err != nil {
			return err
		}
		fmt.Printf("closed %s/%s\n", project, role)
		return nil
	}
	ws, err := core.ListWindows(project)
	if err != nil {
		return err
	}
	fmt.Printf("kill project %s with %d terminal(s)? [y/N] ", project, len(ws))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		fmt.Println("aborted")
		return nil
	}
	// Remember who was viewing: their foreman-opened windows become husks
	// the moment the session dies (issue #43). The cleanup must survive this
	// very process — run from inside the project, kill-session takes our own
	// pane with it — so a detached worker does the closing (issue #46).
	viewers, _ := core.Tmux("list-clients", "-t", core.SessionName(project), "-F", "#{client_name}")
	spawnCloseWorker(strings.Split(viewers, "\n"))
	core.ForgetParticipants(project, "")
	if _, err := core.Tmux("kill-session", "-t", "="+core.SessionName(project)); err != nil {
		return err
	}
	fmt.Printf("closed project %s\n", project)
	return nil
}

// spawnCloseWorker starts a detached __closewins process (its own session,
// no inherited stdio) so tracked-window cleanup survives kill-session taking
// this process down with the project (issue #46).
func spawnCloseWorker(ttys []string) {
	bin := core.BinaryPath()
	if bin == "" {
		return
	}
	args := []string{"__closewins"}
	for _, t := range ttys {
		if t != "" {
			args = append(args, t)
		}
	}
	if len(args) == 1 {
		return
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Start()
}

func cmdPin(project string, on bool) error {
	if !core.SessionExists(project) {
		return fmt.Errorf("no project %s", project)
	}
	val := ""
	if on {
		val = "1"
	}
	// set-option does not accept the "=" exact-match target prefix.
	if _, err := core.Tmux("set-option", "-t", core.SessionName(project), "@fm_priority", val); err != nil {
		return err
	}
	core.StyleSession(project)
	if on {
		fmt.Printf("pinned %s\n", project)
	} else {
		fmt.Printf("unpinned %s\n", project)
	}
	return nil
}

func cmdRename(project, newName string) error {
	if err := core.RenameProject(project, newName); err != nil {
		return err
	}
	fmt.Printf("renamed %s to %s\n", project, newName)
	return nil
}

func cmdList(project string) error {
	if !core.SessionExists(project) {
		return fmt.Errorf("no project %s", project)
	}
	ws, err := core.ListWindows(project)
	if err != nil {
		return err
	}
	cfg := core.LoadConfig()
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TERMINAL\tSTATE\tRUNNING\tACTIVITY")
	for _, w := range ws {
		st := core.StateOf(cfg, w)
		fmt.Fprintf(tw, "%s\t%s%s\t%s\t%s\n",
			w.RoleName(), st, stateMark(st), core.WindowStatus(w), core.HumanAge(w.Activity))
	}
	return tw.Flush()
}

// stateMark flags the states that want a glance; working stays quiet.
func stateMark(s core.State) string {
	switch s {
	case core.StateWaiting:
		return "⚠"
	case core.StateDone:
		return "○"
	}
	return ""
}

// cmdTicker prints the cross-project attention line for one status bar.
// Runs on tmux's redraw schedule, so it must stay quiet and cheap.
func cmdTicker(current string) error {
	projects, err := core.ListProjects()
	if err != nil || len(projects) == 0 {
		return nil
	}
	fmt.Print(core.TickerLine(core.LoadConfig(), projects, current))
	return nil
}

func cmdListAll() error {
	projects, err := core.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Println("no projects")
		return nil
	}
	cfg := core.LoadConfig()
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, p := range projects {
		marker := " "
		if p.Pinned {
			marker = "*"
		}
		attached := ""
		if p.Attached {
			attached = " (attached)"
			if p.Clients > 1 {
				attached = fmt.Sprintf(" (attached ×%d)", p.Clients)
			}
		}
		var parts []string
		for _, w := range p.Windows {
			parts = append(parts, fmt.Sprintf("%s%s:%s",
				w.RoleName(), stateMark(core.StateOf(cfg, w)), core.WindowStatus(w)))
		}
		plural := "s"
		if len(p.Windows) == 1 {
			plural = ""
		}
		fmt.Fprintf(tw, "%s %s\t%d terminal%s%s\t%s\n", marker, p.Name, len(p.Windows), plural, attached, strings.Join(parts, "  "))
	}
	return tw.Flush()
}
