# Development workflow

> Status: **current model (2026-07-08).** Deliberately lightweight for a solo developer, adopted
> from the NamWeb workflow (`NamWeb/docs/ops/workflow.md`) and trimmed to fit a local CLI tool:
> foreman has no production environment — a "release" is a git tag and a GitHub Release, and the
> blast radius of a bad merge is one laptop. Companion to [RELEASING.md](./RELEASING.md).

## Issues are the unit of work

Every non-trivial change is a **GitHub issue** before it is code. The issue holds the motivation
and the design discussion; the commit/PR closes it (`Closes #N`). Small drive-by fixes may skip
the issue, but anything worth a CHANGELOG entry is worth an issue.

Labels: the GitHub defaults (`bug`, `enhancement`, `documentation`, …) plus domain labels —
`core` (tmux layer, `internal/core`), `cli` (command surface), `explorer` (menu bar / tray app),
`config` (config.toml / role presets), `observation` (the observation-layer epic), `infra`
(build, tooling, CI).

## Sprints are milestones

Work is grouped into **sprints, one GitHub milestone each** — either numbered
(`Sprint N — Theme`) or thematic (`Observation layer`, `Polish 1`). The milestone description
states the goal in a sentence or two. A sprint is done when its issues are closed; the end of a
sprint is the natural moment to cut a release (see [RELEASING.md](./RELEASING.md)).

Not everything needs a sprint — off-cycle fixes ride on `main` and fold into the next release.

## Branches and merging

1. Branch off `main` per issue (`feature/<slug>` or `fix/<slug>`).
2. Develop locally; `make check` (gofmt + vet + test) must pass.
3. Open a PR and merge to `main`. CI (`.github/workflows/check.yml`) runs `make check` on every
   PR and on `main` — the automated gate. Review is self-review — the PR still earns its keep
   as the changelog of record and the thing a Codex/Claude review can be pointed at.
4. Every merged non-chore PR adds its entry under `## [Unreleased]` in `CHANGELOG.md`.

`main` is **branch-protected**: the `check` job is a required status check (enforced for admins),
so nothing merges red and a direct push to `main` is gated behind a green PR; force-pushes and
branch deletion are blocked. This is a *workflow gate*, not a security control — its only security
edge is that history can't be silently rewritten. For a genuine emergency, toggle enforcement off
under **Settings → Branches**, merge, and turn it back on.

## Repository protections

Standing GitHub hardening — mostly workflow-gate, a little tamper-resistance:

- **Branch protection on `main`** — the quality gate above.
- **Secret scanning + push protection** — blocks a commit that carries a recognizable token
  before it can be pushed.
- **Dependabot** — vulnerability alerts and auto-PRs for vulnerable Go dependencies.

After a spam/malware issue comment (a throwaway account attaching a "just run this" zip, July
2026), **interaction limits** were set to *prior contributors only* — a temporary lever that
auto-expires, aimed at drive-by accounts. The real defense against that class of thing isn't a
repo setting: never run code from an unsolicited attachment.

## Evolving this

Current gaps, to add when they start paying for themselves:

- **Release automation** — a `release.yml` that publishes a GitHub Release (with prebuilt
  binaries) from a `v*` tag, as NamWeb does.
