<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo/logo-wordmark.svg">
    <img src="docs/assets/logo/logo-wordmark-light.svg" width="420" alt="foreman — terminals by project">
  </picture>
</p>

Organize terminals by **project**, on top of tmux.
New to the terminal-server idea? Read [how foreman works](docs/how-it-works.md).

Modern dev work is terminal-heavy, and AI agents made it worse: several projects
run at once (you work on B while A's agents execute), each with a mix of agent
terminals and manual shells. Terminals get hunted for, typed into by mistake,
and closed by accident. foreman gives them an organizing principle — every
terminal is a named *participant* in a *project*.

```
foreman init                     write a starter config to edit
foreman list                     all projects and their terminals
foreman <project> new <role>     new terminal in the project (runs role preset, if any)
foreman <project> go <role>      move that terminal's view here (--mirror to view alongside,
                                 --window to open a new terminal window instead)
foreman <project> list           terminals in the project
foreman <project> adopt <role>   pull the terminal you're in into the project (works
                                 outside tmux too — see below)
foreman <project> done [role]    close one terminal, or the whole project
foreman <project> pin|unpin      mark the project as priority (sorts first in list)
foreman <project> rename <name>  give the project a new name (updates config too)
```

The project name can be omitted — `foreman new coder` infers it: inside a
managed terminal, that terminal's project; otherwise the configured project
`root` containing your current directory; otherwise the directory's name.
(Verb names — `new`, `go`, `list`, … — are therefore reserved as project
names. Bare `foreman list` always shows all projects.)

After creating a terminal, `new` offers to jump to it (`go to coder? [Y/n]`,
plain Enter goes). The prompt only appears on a TTY — scripted use creates
without going.

`adopt` also works from a plain terminal opened the usual way. A running
shell can never be *moved* into tmux (the OS won't re-wire a live process),
so foreman does the closest honest thing: it creates a new participant whose
shell starts in your current directory and attaches the window you're in as
its viewer — badge, observation, and explorer visibility included. Detach
and the shell you were in comes back; only a program *already running* in
that shell stays outside tmux.

Example session:

```
$ foreman A new planner          # starts claude in planner mode (role preset)
$ foreman A new shell            # plain shell for manual work
$ foreman A go planner           # jump there — attaches if outside tmux, switches if inside
$ foreman list
* A  2 terminals (attached)  planner:claude  shell:shell
  B  1 terminal              reviewer:claude
```

## How it maps to tmux

- **Project = tmux session** (named `fm-<project>` internally; you always type the bare name).
- **Terminal = tmux window**, tagged with a `@fm_role` user option. Lookups go by
  role metadata and immutable window id, never by display name — so `go planner`
  still works if something renames the window.
- **Identity is loud**: every managed session gets a colored status-bar badge with
  the project name (color is stable per project), so you always know what you're
  typing into.
- **One place at a time**: `go` moves the project's view to the terminal you ran
  it in, detaching other viewers — navigating never multiplies terminals.
  `go <role> --mirror` keeps the other views for the rare side-by-side case.
- **Accidental close is harmless**: closing a terminal window only detaches a
  client; the session lives on in the tmux server. The only real teardown is the
  explicit `done` verb (whole-project `done` asks for confirmation).
- All state lives in the tmux server (user options `@fm_project`, `@fm_role`,
  `@fm_priority`) — no state files, nothing to drift out of sync.

## Install

Requires `tmux` — `brew install tmux` on macOS, `sudo apt install tmux` on
Ubuntu, `sudo dnf install tmux` on Fedora.

```
make install                    # foreman + fm symlink + fm-explorer into ~/.local/bin
```

(`make help` lists the other targets; `BIN=/somewhere make install` to install
elsewhere on PATH.)

## Explorer (menu bar / tray app)

A menu bar (macOS) and system tray (Linux) companion: glance at every project and participant, click one
to pull its view into a terminal window (same move semantics as `go`). Each
project's menu also *acts*: create a participant or a brand-new project by
name, pin/unpin, rename, or end one (behind a confirmation) — every action is
the matching `fm` command, so the explorer never knows anything the CLI
doesn't.

Two ways to run it (installed by `make install`; either way it shows the
foreman mark in the menu bar or tray):

```
fm-explorer install-agent       # recommended: starts now and on every login,
                                # and restarts itself if it ever crashes
fm-explorer &                   # quick and manual — but dies with the terminal
                                # you launch it from (use `nohup fm-explorer &`
                                # to outlive it)
```

`install-agent` registers a login item — a LaunchAgent on macOS, a systemd
user unit (`foreman-explorer.service`) on Linux; `uninstall-agent` removes it.
Quitting from the menu stays quit — the agent only relaunches on a crash, not
on a deliberate Quit. Crashes are logged to
`~/.local/state/foreman/explorer.log` (on Linux also
`journalctl --user -u foreman-explorer`).

On Linux the explorer needs a tray and, for its dialogs (Done…, Rename…,
New project…), `zenity`:

- **Ubuntu** (GNOME) ships both — the tray via the preinstalled AppIndicator
  extension.
- **Fedora** (GNOME) ships neither:
  `sudo dnf install zenity gnome-shell-extension-appindicator`, then
  `gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com` and log out
  and back in.
- **KDE Plasma** has a tray built in; install `zenity` (Kubuntu, Fedora KDE).

The menu re-reads state every 10 seconds — participant labels update in place,
and the menu is only rebuilt when projects or participants come or go, so it
never closes under your cursor while you navigate. Pinned projects show a ★.

## Config

`foreman init` writes a starter `~/.config/foreman/config.toml` (optional;
override path with `$FOREMAN_CONFIG`):

```toml
[role.planner]
cmd = "claude --permission-mode plan"

[role.reviewer]
cmd = "claude"

[project.A]
root = "~/code/project-a"

[terminal]
app = "terminal"                # macOS; on Linux e.g. "ptyxis"
```

`new` starts terminals in the role's `dir`, else the project `root`, else your
current directory. Role commands run *inside* the shell, so the window survives
the process exiting. Unknown roles just get a plain shell — manual-work
terminals are first-class participants.

`[terminal] app` picks the app foreman opens windows in (explorer clicks,
`go --window`). On macOS: `terminal` for Terminal.app, the default when unset,
or `ghostty`. On Linux: `xdg-terminal-exec`, `ptyxis`, `gnome-terminal`,
`konsole` or `ghostty` (1.2+); unset picks the first of these on PATH, in that
order.

## Observation

foreman tells you what every participant is doing without attaching: `foreman list` shows a
STATE per terminal (`working`, `waiting⚠`, `done○`, `shell`, `exited`), the status bar of
each managed terminal tickers who's waiting in *other* projects, and the explorer posts a
desktop notification when an agent in a **pinned** project starts waiting. Detection reads
what agents announce (Claude Code signals busy/idle in its pane title) with a careful
content fallback — see [how it works](docs/how-it-works.md).

Windows that foreman opens (explorer clicks, `go --window`) are closed again
automatically once their view moves elsewhere — no `[detached]` husks pile up. Windows you
opened yourself are never touched.

## Roadmap

Tracked as [GitHub issues](https://github.com/Aha43/Foreman/issues), grouped into sprint
milestones — see [docs/workflow.md](docs/workflow.md). The broad strokes:

- tmux control mode (`tmux -CC`) event stream instead of polling, once the
  heuristics settle.

## License

[MIT](LICENSE)
