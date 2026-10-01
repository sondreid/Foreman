// foreman-explorer is a menu bar (macOS) and tray (Linux) app over foreman's
// core: glance at projects and their participants, click one to pull its
// view into a terminal window. Platform integration — dialogs,
// notifications, the icon, the login agent — lives in the _darwin and
// _linux files.
package main

import (
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/systray"

	"github.com/arnehalvorsen/foreman/internal/core"
)

func main() {
	if len(os.Args) > 1 {
		runCommand(os.Args[1])
		return
	}
	setupCrashLog()
	systray.Run(onReady, nil)
}

// runCommand handles the non-menu invocations — the launchd agent lifecycle
// (issue #80). Bare `fm-explorer` still just runs the menu.
func runCommand(cmd string) {
	switch cmd {
	case "install-agent":
		fail(installAgent())
		fmt.Println("foreman explorer installed as a login agent — running now, and on every login.")
		fmt.Printf("logs: %s\n", logPath())
	case "uninstall-agent":
		fail(uninstallAgent())
		fmt.Println("foreman explorer login agent removed.")
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q — use install-agent or uninstall-agent\n", cmd)
		os.Exit(1)
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// setupCrashLog tees the standard logger to the explorer's log file *and*
// stderr, so a panic (below) leaves evidence on disk for a launchd/vanished
// run yet still prints to the terminal when run in the foreground to debug.
// A log we can't open is not fatal — stderr alone still matters more.
func setupCrashLog() {
	os.MkdirAll(stateDir(), 0o755)
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func onReady() {
	setTrayIcon()
	systray.SetTooltip("foreman — terminals by project")
	// All menu work happens on this one goroutine — the timer and manual
	// Refresh both feed it, so the done channel and the systray menu are
	// never touched concurrently (issue #31). A panic here would silently
	// drop the menu bar icon; log it and exit non-zero so the evidence is
	// on disk and a launchd agent (issue #80) restarts us.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("explorer refresher panic: %v\n%s", r, debug.Stack())
				os.Exit(1)
			}
		}()
		refresh(false)
		tick := time.Tick(10 * time.Second)
		for {
			var force bool
			select {
			case <-tick:
			case <-rebuildReq:
				force = forcePending.Swap(false)
			}
			refresh(force)
		}
	}()
}

// rebuildReq carries manual refresh requests to the rebuilder goroutine;
// capacity 1 makes extra clicks while a rebuild is pending no-ops.
// forcePending rides alongside it: the Refresh button wants an unconditional
// rebuild even when the shape is unchanged, so it can repair a menu that
// looks wrong without a shape-changing event (issue #79 review).
var rebuildReq = make(chan struct{}, 1)
var forcePending atomic.Bool

func requestRebuild() {
	select {
	case rebuildReq <- struct{}{}:
	default:
	}
}

// requestForceRebuild asks for a full ResetMenu rebuild regardless of shape.
func requestForceRebuild() {
	forcePending.Store(true)
	requestRebuild()
}

// done is closed on every menu rebuild so click-listener goroutines from the
// previous menu generation exit instead of leaking. lastShape fingerprints
// the structure last built; participantItems are the live handles for
// in-place label updates between rebuilds (keyed by tmux window id), and
// shownLabels what each currently reads — an unchanged label isn't re-set,
// since on Linux every set is two D-Bus signals. Only the refresher
// goroutine touches any of them.
var done chan struct{}
var lastShape = "\x00never built"
var participantItems map[string]*systray.MenuItem
var shownLabels map[string]string

// refresh is one tick: sweep, observe every participant (the notification
// pass), then render. The menu is torn down and rebuilt only when its shape
// — the set of projects, participants, pins, dormants — changed; label-only
// changes (an agent flipping between working and waiting, status text)
// update the existing items in place, so an open menu is never yanked shut
// under the user's cursor (issue #79). Action clicks change the shape, and
// the click already closed the menu — their refresh still rebuilds.
func refresh(force bool) {
	// Collect windows whose client ended without passing through go/done.
	core.SweepTermWindows()

	cfg := core.LoadConfig()
	projects, _ := core.ListProjects()
	labels := observe(cfg, projects)
	// Compute the config-derived lists once and thread them through — both
	// the fingerprint and the menu build need them (issue #79 review).
	roles := roleChoices(cfg)
	dormant := dormantNames(cfg, projects)
	if shape := menuShape(projects, roles, dormant); force || shape != lastShape {
		lastShape = shape
		rebuildMenu(projects, labels, roles, dormant)
		return
	}
	for id, item := range participantItems {
		if label, ok := labels[id]; ok && label != shownLabels[id] {
			item.SetTitle(label)
			shownLabels[id] = label
		}
	}
}

// observe runs the watch pass over every participant — state memory and
// waiting-notifications happen every tick no matter how the menu renders —
// and returns each window's fresh display label, keyed by window id.
func observe(cfg core.Config, projects []core.Project) map[string]string {
	labels := map[string]string{}
	for _, p := range projects {
		for _, w := range p.Windows {
			st := watch(cfg, p, w)
			labels[w.ID] = fmt.Sprintf("   %s%s — %s", w.RoleName(), stateMark(st), core.WindowStatus(w))
		}
	}
	return labels
}

// menuShape fingerprints everything whose change requires re-adding menu
// items — systray can retitle items in place, but not change membership:
// the projects (order and pin state), their participants (id and role),
// and the config-derived entries (role choices, dormant projects). Every
// field is NUL-terminated; NUL can't occur in a tmux name, id, or config
// key, so two different structures can't collide to one fingerprint the way
// in-band delimiters (|, ;, =) could (issue #79 review).
func menuShape(projects []core.Project, roles, dormant []string) string {
	var b strings.Builder
	field := func(s string) { b.WriteString(s); b.WriteByte(0) }
	for _, p := range projects {
		field(p.Name)
		field(fmt.Sprintf("%v", p.Pinned))
		for _, w := range p.Windows {
			field(w.ID)
			field(w.RoleName())
		}
		field("\x1e") // end of project
	}
	field("\x1e")
	for _, r := range roles {
		field(r)
	}
	field("\x1e")
	for _, name := range dormant {
		field(name)
	}
	return b.String()
}

func rebuildMenu(projects []core.Project, labels map[string]string, roles, dormant []string) {
	if done != nil {
		close(done)
	}
	done = make(chan struct{})
	systray.ResetMenu()
	participantItems = map[string]*systray.MenuItem{}
	shownLabels = map[string]string{}

	if len(projects) == 0 {
		systray.AddMenuItem("no projects", "").Disable()
	}
	for _, p := range projects {
		buildProjectMenu(p, labels, roles, done)
		systray.AddSeparator()
	}
	buildDormant(dormant, roles, done)
	buildNewProject(roles, done)

	listen(systray.AddMenuItem("Refresh", ""), done, requestForceRebuild)
	listen(systray.AddMenuItem("Quit", ""), done, systray.Quit)
}

// buildProjectMenu adds one project's block to the menu: a header opening a
// submenu of actions on the project, then one top-level item per participant
// — participants stay at the top level so the at-a-glance view and the
// one-click "pull it here" survive the actions moving in (issue #68).
func buildProjectMenu(p core.Project, labels map[string]string, roles []string, d chan struct{}) {
	title := p.Name
	if p.Pinned {
		title = "★ " + title
	}
	// The header must stay enabled for its submenu to open; it has no click
	// listener, so selecting the header itself still does nothing.
	header := systray.AddMenuItem(title, "project actions")
	buildNewParticipant(p, header, roles, d)
	// Pin/unpin is one toggle, labeled by what a click would do — judged
	// from the project's current @fm_priority (issue #71).
	pinLabel, pinVerb := "Pin", "pin"
	if p.Pinned {
		pinLabel, pinVerb = "Unpin", "unpin"
	}
	pin := header.AddSubMenuItem(pinLabel, "pinned projects sort first and post notifications")
	project, verb := p.Name, pinVerb
	listen(pin, d, func() {
		execFm(project, verb)
		requestRebuild()
	})
	addRenameItem(header, p.Name, d)
	// Done entries sit behind the header submenu and a confirmation dialog —
	// two steps from the top-level go-click, on purpose (issue #72). The
	// ellipsis is the macOS "asks before acting" convention.
	if len(p.Windows) > 0 {
		header.AddSeparator()
	}
	for _, w := range p.Windows {
		item := header.AddSubMenuItem("Done "+w.RoleName()+"…", "end this participant — closes its terminal for real")
		project, role := p.Name, w.RoleName()
		listen(item, d, func() {
			if confirmDone(project, role) {
				execFm(project, "done", role)
				requestRebuild()
			}
		})
	}
	for _, w := range p.Windows {
		item := systray.AddMenuItem(labels[w.ID], "click to open here")
		participantItems[w.ID] = item
		shownLabels[w.ID] = labels[w.ID]
		project, role := p.Name, w.RoleName()
		listen(item, d, func() { openInTerminal(project, role) })
	}
}

// buildNewParticipant fills the project's actions submenu with "New
// participant" role entries. Roles the project already has are shown
// disabled — new would only refuse them (issue #69).
func buildNewParticipant(p core.Project, header *systray.MenuItem, roles []string, d chan struct{}) {
	menu := header.AddSubMenuItem("New participant", "start a terminal in this project")
	have := map[string]bool{}
	for _, w := range p.Windows {
		have[w.RoleName()] = true
	}
	addRoleItems(menu, p.Name, roles, have, d)
}

// addRoleItems adds one "start this role" entry per role under menu, each
// creating a participant in project on click; roles in have are shown
// disabled. Shared by the live-project and dormant-project menus so they
// can't drift (issue #79 review).
func addRoleItems(menu *systray.MenuItem, project string, roles []string, have map[string]bool, d chan struct{}) {
	for _, r := range roles {
		item := menu.AddSubMenuItem(r, "")
		if have[r] {
			item.Disable()
			continue
		}
		role := r
		listen(item, d, func() { newParticipant(project, role) })
	}
}

// roleChoices is what a new participant may start as: every configured role
// plus shell — unknown roles get a plain shell, so it is always valid.
func roleChoices(cfg core.Config) []string {
	roles := make([]string, 0, len(cfg.Roles)+1)
	for r := range cfg.Roles {
		roles = append(roles, r)
	}
	if _, ok := cfg.Roles["shell"]; !ok {
		roles = append(roles, "shell")
	}
	sort.Strings(roles)
	return roles
}

// buildDormant lists configured projects with no live session — "create new
// project" without inventing anything: a project exists when its session
// does, and starting one is just new on a project not running yet
// (issue #70). Each dormant project opens the same role choices as "New
// participant"; the first participant brings the project to life.
func buildDormant(dormant, roles []string, d chan struct{}) {
	if len(dormant) == 0 {
		return
	}
	systray.AddMenuItem("start a project", "configured, not running").Disable()
	for _, name := range dormant {
		menu := systray.AddMenuItem("   "+name, "start this project")
		addRoleItems(menu, name, roles, nil, d)
		menu.AddSeparator()
		addRenameItem(menu, name, d)
	}
	systray.AddSeparator()
}

// buildNewProject adds the "New project…" submenu: pick the first
// participant's role, then name the project in a dialog — a project the
// config doesn't know about yet, created by typed name (issue #73). The
// name is validated up front: an invalid one must never reach the CLI,
// where a reserved name like "list" would dispatch as the command instead
// of the project.
func buildNewProject(roles []string, d chan struct{}) {
	menu := systray.AddMenuItem("New project…", "create a project by name")
	for _, r := range roles {
		item := menu.AddSubMenuItem(r, "start the project with this participant")
		role := r
		listen(item, d, func() {
			name := promptText(fmt.Sprintf("Name the new project (first participant: %s):", role), "", "Create")
			if name == "" {
				return
			}
			if err := core.ValidateProjectName(name); err != nil {
				notify(err.Error())
				return
			}
			newParticipant(name, role)
		})
	}
	systray.AddSeparator()
}

// dormantNames is the configured projects with no live session, sorted.
func dormantNames(cfg core.Config, projects []core.Project) []string {
	live := map[string]bool{}
	for _, p := range projects {
		live[p.Name] = true
	}
	var dormant []string
	for name := range cfg.Projects {
		if !live[name] {
			dormant = append(dormant, name)
		}
	}
	sort.Strings(dormant)
	return dormant
}

// addRenameItem adds a "Rename…" entry under menu, prompting for the new
// name and handing it to the CLI's rename verb — live or dormant, the CLI
// decides what the rename means. Shared by both menus so they can't drift.
func addRenameItem(menu *systray.MenuItem, project string, d chan struct{}) {
	item := menu.AddSubMenuItem("Rename…", "rename the project — session and config follow")
	listen(item, d, func() {
		newName := promptText(fmt.Sprintf("Rename project %s to:", project), project, "Rename")
		if newName != "" && newName != project {
			execFm(project, "rename", newName)
			requestRebuild()
		}
	})
}

// newParticipant creates the terminal (fm new without a TTY creates without
// going), then pulls it into a Terminal window — you create a terminal to
// use it. The menu refreshes either way so it reflects whatever actually
// happened.
func newParticipant(project, role string) {
	if execFm(project, "new", role) {
		openInTerminal(project, role)
	}
	requestRebuild()
}

// execFm runs an fm verb silently and reports whether it succeeded; a
// failure surfaces the CLI's own message as a notification.
func execFm(args ...string) bool {
	cmd := exec.Command(fmBinary(), args...)
	// The explorer's own cwd is meaningless ("/" under launchd) — run fm
	// from $HOME so new's cwd fallback for a project with no configured
	// root lands somewhere sensible (issue #73).
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	notify(msg)
	return false
}

// listen fires fn on every click of item until this menu generation is torn
// down (d closes), so listener goroutines never outlive their menu. A rebuild
// also closes ClickedCh; a listener still inside a dialog would otherwise
// find both cases ready, and select picking the closed channel would run fn
// a second time.
func listen(item *systray.MenuItem, d chan struct{}, fn func()) {
	go func() {
		for {
			select {
			case _, ok := <-item.ClickedCh:
				if !ok {
					return
				}
				safely(fn)
			case <-d:
				return
			}
		}
	}()
}

// safely runs a click handler so a panic in one action is logged and
// contained — it can't take the whole menu bar app down with it (issue #80).
func safely(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("explorer action panic: %v\n%s", r, debug.Stack())
		}
	}()
	fn()
}

// watch is the observation epic's memory half (issue #5): compare this
// look with the last one (stored as a tmux window option), record the new
// state, and notify on a transition into waiting — pinned projects only,
// everything else stays glanceable in this menu. Runs on the single
// refresher goroutine, every 10s poll.
func watch(cfg core.Config, p core.Project, w core.Window) core.State {
	st := core.StateOf(cfg, w)
	last := core.LastState(w)
	if st != last {
		core.RecordState(w, st)
		// last == "" is the first look at this window: record silently.
		if last != "" && st == core.StateWaiting && p.Pinned {
			notify(fmt.Sprintf("%s is waiting in %s", w.RoleName(), p.Name))
		}
	}
	return st
}

func stateMark(s core.State) string {
	switch s {
	case core.StateWaiting:
		return "⚠"
	case core.StateDone:
		return "○"
	}
	return ""
}

// openInTerminal opens a window in the configured terminal app running
// "fm <project> go <role>"; with go's move semantics the participant's view
// lands there and any other viewer detaches. The window closes again once
// its view moves away (issue #43). Every shell argument is quoted (issue #30).
func openInTerminal(project, role string) {
	cmd := fmt.Sprintf("%s %s go %s", core.ShellQuote(fmBinary()), core.ShellQuote(project), core.ShellQuote(role))
	if err := core.OpenTerminal(core.LoadConfig().Terminal, cmd); err != nil {
		notify(err.Error())
	}
}

// fmBinary locates the fm CLI: the sibling binary next to this executable
// (how make install lays them out), else whatever PATH resolves.
func fmBinary() string {
	if exe, err := os.Executable(); err == nil {
		if sibling := filepath.Join(filepath.Dir(exe), "fm"); exists(sibling) {
			return sibling
		}
	}
	return "fm"
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
