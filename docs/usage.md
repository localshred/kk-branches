# kk-branches usage

`kk-branches` is invoked through `kk` as `kk branches [flags]`. It can also
be run directly as `kk-branches [flags]` if the binary is on `PATH` (useful
for local development). It operates on the repository containing the
current directory — any worktree of it works, not just the main checkout.

## Commands

### `kk branches` (interactive)

Opens the table and stays open until you quit. The header shows the repo
name, trunk branch, and time since the last fetch (or a spinner while a
sync is running). Below it: a text filter line, one row per local branch,
and a footer with the selection count and key hints.

Columns, left to right:

- select glyph — `★` trunk, `◉` selected, `○` neither
- `BRANCH` — local name, truncated in the middle if long; if its upstream
  has a different name, a dimmed `→ upstream` follows it
- `ORIGIN` — `=` in sync, `↑n`/`↓n` ahead/behind (both if diverged), `gone`
  (upstream deleted), or `local only` (never pushed)
- `PR` — a cmd-clickable `#number`, its state (`open`, `merged`, `closed`),
  a checks glyph when open, or `— none`; trunk rows show `trunk`
- `WORKTREE` — the worktree's path relative to the repo's parent directory,
  `(main checkout)` for the primary one, with `✎ dirty` appended when it
  has uncommitted or untracked changes
- `AGE` — age of the branch's last commit
- `SAFE DELETION?` — `safe`, `confirm`, `blocked`, or blank for trunks
  (see Risk levels below); once a sync has run, the column is relabeled
  `SYNC RESULT` and shows each branch's sync outcome until `r` reloads

Keys:

- `↑`/`k`, `↓`/`j` — move the cursor
- `space` — select or unselect the highlighted row (safe and confirm rows
  only; blocked rows can't be selected)
- `a` — select every safe row currently visible under the filter
- `tab` / `shift+tab` — cycle the filter preset forward or back
- `/` — focus the text filter (`enter` keeps it, `esc` clears it); it
  matches branch names case-insensitively
- `d` / `c` — delete the selection, or the highlighted row if nothing is
  selected
- `w` — add a worktree for the highlighted row
- `W` — remove the highlighted row's worktree
- `n` — open the new-branch dialog
- `s` — sync every branch with origin
- `o` — open the highlighted row's PR in the browser
- `i` — open the info panel, which lists why the highlighted row is safe,
  confirm, or blocked, plus its last sync result; it follows the cursor as
  you move, and `i` or `esc` closes it
- `r` — reload
- `?` — toggle the help overlay
- `q` / `ctrl+c` — quit

Filter presets, in cycle order: `all`, `merged`, `gone`, `local-only`,
`worktree`, `dirty`, `stale` (last commit older than 30 days).

### `kk branches --print`

Loads the same snapshot the interactive table uses — branches, their
origin status, and their pull requests — and prints it once with the same
columns, then exits. There is no filter, selection, or risk-based access to
actions; it is meant for scripts, piping, or a quick look without opening
the TUI.

If the pull-request lookup fails (`gh` missing, unauthenticated, or the API
call itself fails), the table still prints — every `PR` cell falls back to
`?` — and a dim note about the failure goes to stderr instead of aborting.

## Risk levels and their reasons

Every local branch except a trunk is a deletion candidate; trunks never
show a risk word. Deletion compares the branch against its pull request
rather than trusting git's merged-branch detection, since squash merges
break ancestry checks against the trunk.

- **safe** — the branch's PR is merged, and the branch's tip equals, or is
  an ancestor of, the PR's last head commit. A batch of safe selections
  gets one combined confirmation.
- **confirm** — one or more of: never pushed, no PR found, the PR closed
  without merging, the PR merged but the branch has commits the PR never
  saw, or commits ahead of origin. Each reason is listed so you know why.
  Confirm deletions use a separate confirmation step and force-delete the
  branch.
- **blocked** — the branch's worktree is dirty, or the branch is checked
  out in the main checkout or in the worktree `kk-branches` is running
  from. Blocked branches can't be selected for deletion at all.
- **pending** (shown as `…`) — the pull-request lookup for this branch
  hasn't completed yet.

A clean worktree holding the branch being deleted is removed first, so
"checked out in a worktree" never blocks a delete by itself — only a dirty
or current one does. The confirmation dialog lists each worktree a delete
will remove.

Deletion rechecks the live repository rather than trusting the table: if
the branch has moved since the table loaded, or its worktree has since
switched to another branch, the delete is refused with a message to press
`r` and reload. A moved branch keeps its worktree.

## Sync (`s`)

Sync reproduces the `reposync` shell routine, with a few differences:

- It refuses to start while a rebase, merge, cherry-pick, or bisect is in
  progress in the worktree `kk-branches` runs from; finish or abort it
  first.
- It fetches `origin --prune`, then rebases every local branch that has a
  live upstream onto that upstream — using each branch's actual tracked
  upstream, not an assumed `origin/<local name>`.
- A branch checked out in another worktree is rebased inside that
  worktree, not the main checkout. A branch checked out nowhere is
  fast-forwarded in place when it can be; if git refuses (say, the branch
  was checked out elsewhere after the table loaded), it is skipped with
  git's reason.
- Changes in the worktree `kk-branches` runs from — not necessarily the
  main checkout — are autostashed (including untracked files) before
  syncing and popped afterward, same as `reposync`. Every other worktree,
  including the main checkout when you run from somewhere else, is
  skipped if dirty — never stashed or rebased — and otherwise rebased in
  place, because another process (a Claude session, for example) may be
  mid-edit there. A plain rebase loop has no such protection.
- Only the autostash `kk-branches` itself pushed is popped, found by its
  commit in `git stash list`, so a stash another process pushes meanwhile
  is left alone. If the starting branch can't be checked back out, the
  stash is kept rather than popped onto the wrong branch.
- Progress streams per branch: `rebased +n`, `up to date`, `skipped:
  <reason>`, or `conflict: aborted`. The header shows a spinner and a
  `syncing n/total` counter while it runs. Navigation keys keep working,
  but mutating keys are disabled until the sync finishes.
- If the stash pop conflicts afterward, that's reported loudly and the
  stash is left in place for you to resolve by hand — `kk-branches` never
  forces through a stash conflict.

## Worktree path rule

Adding a worktree for a branch always creates it beside the main checkout,
named `<repo>-wt-<branch>`, with the **entire** branch name and every `/`
turned into a `-`. For example, branch `alice/fix-login` in a `my-repo`
checkout becomes `../my-repo-wt-alice-fix-login`.

If that path already exists, `kk-branches` says so and stops — it never
guesses a different name or auto-renames to avoid the collision. Removing
a worktree never passes `--force`, and refuses on a dirty worktree, the
main checkout, the worktree `kk-branches` is currently running from, or a
worktree that has switched off the branch since the table loaded.

## Configuration

- `kk.trunks` — a multi-valued git config key (read at any level: global,
  or per-repo) naming extra branches to treat as trunks, in addition to
  the repo's default branch (from `origin/HEAD`) and `main`/`master`.
  Trunks are never deletion candidates.
- `kk.branchPrefix` — a git config key that prefills the name field in the
  new-branch dialog, e.g. `alice/`. When unset, the prefix is derived from
  your existing local branch names instead.
- `kk.releasePattern` — a multi-valued git config key of branch globs,
  relative to `origin`, that the new-branch dialog offers as bases (newest
  first by version sort, up to five). Defaults to `release*` and
  `release*/**`, which match any branch whose name starts with `release`.
  Set it per repo to match your own scheme, e.g.
  `git config kk.releasePattern 'stable-*'`. Setting it replaces the
  defaults.
