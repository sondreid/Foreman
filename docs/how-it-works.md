# How foreman works

> For readers who haven't worked much with tmux or terminal multiplexers. No prior
> knowledge assumed; every concept arrives when it's needed.

## The star of the show: the terminal server

Here's the realization foreman is built on: **the terminal window on your screen and the
thing running inside it don't have to be the same object.**

Normally they are. You open a Terminal window, a shell starts inside it, and they live and
die together — close the window and everything running in it is gone. That coupling is why
terminals feel fragile: one stray ⌘W and your agent's afternoon of work vanishes.

tmux breaks the coupling. It's a small **server** that runs quietly in the background and
*owns* your terminals. What you see in a Terminal window is just a **viewer** — a window
into the server. Close the viewer? Nothing happens to the terminal inside; it keeps running
on the server, and you can look at it again whenever you like. Reboot Terminal.app entirely?
Same answer.

Once you've internalized that model — durable terminals living in a server, disposable
windows for looking at them — you'll want it for everything. Anyone who uses a terminal more
than occasionally probably should. tmux spent twenty years pigeonholed as "that thing for
keeping SSH sessions alive"; the AI-agent era, where everyone suddenly has five long-running
processes they care about, makes the local case obvious.

## What foreman adds: a way to think about it

Raw tmux asks you to think in *its* vocabulary: sessions, windows, panes, prefix keys.
Powerful, but it has no opinion about what any of it *means*.

foreman is a deliberately thin layer that renames the concepts to what you actually mean:

| you think            | foreman says    | tmux has        |
|----------------------|-----------------|-----------------|
| a thing I'm building | **project**     | session         |
| a terminal in it     | **participant** | window          |
| what it's for        | **role**        | (a name label)  |

…plus a handful of opinions that follow from taking the model seriously:

- **Identity is loud.** Every managed terminal wears a colored badge naming its project, so
  you never type into the wrong one.
- **One place at a time.** `go` *moves* a project's view to where you are instead of opening
  yet another window — navigating never multiplies terminals.
- **Closing is harmless, ending is explicit.** Closing a window just closes a viewer; the
  only real teardown is the deliberate `done` verb.
- **Manual terminals are first-class.** A plain shell participant is as much a member of the
  project as an agent.

## Where foreman keeps its notes

Nowhere — that's the trick. foreman has no database, no state files. Everything it knows is
written as small labels *onto the tmux objects themselves* (tmux lets you attach named
values to sessions and windows; foreman's all start with `@fm_`). The project a session
belongs to, a participant's role, whether a project is pinned — all labels in the server.

This matters because it means foreman can never disagree with reality. Each `foreman`
command is alive for a fraction of a second: it starts, asks the server questions, prints,
and exits. There is nothing running in between that could hold a stale picture of the world.
The rule the project holds itself to: **interpret tmux state, never duplicate it.**

## Snapshot vs. memory: how the observation layer works

`foreman list` tells you what every participant is *doing* — working, waiting for you, done,
or gone. How can it know, if nothing runs in between commands?

Think of a security guard who only does a walkthrough when you ask. That's a **snapshot**:
each `list` looks at every terminal fresh — is the process still running? what does the
agent's pane title say? (Claude Code helpfully signals its own state there: a spinner while
busy, `✳` when it wants you.) Nothing is remembered afterward; the answer is computed from
the server every time. The status-bar ticker works the same way — and even its scheduling is
borrowed: tmux re-runs the little status command on its own redraw cycle, so no foreman
process needs to exist to keep the ticker fresh.

Notifications are different in kind: "an agent *just started* waiting" is a statement about
**change**, and you can only notice a change by remembering the last look. Something also
has to keep looking. foreman gives both jobs to the explorer (the menu bar or tray app), which wakes
every ten seconds anyway — and true to the rule above, even its memory of "what I saw last
time" is stored as labels on the tmux windows, not in the app.

So: questions get snapshots, changes need memory — and both live in the server.

## The blast radius of things going wrong

- Accidentally closed a Terminal window → nothing happened; `foreman <project> go <role>`
  brings the view back.
- Explorer not running → no notifications, but the ticker, `list`, and everything else are
  unaffected (they never depended on a running foreman).
- tmux server itself stops (reboot, `tmux kill-server`) → that *is* the teardown; the
  terminals were the server's. foreman starts fresh with it, no stale state anywhere.
