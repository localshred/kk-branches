# kk-branches

`kk-branches` is a [koolkit](https://github.com/localshred/koolkit) plugin
for managing local git branches, their pull requests, and worktrees from one
screen. It replaces the usual "sync, check status, copy a branch name by
hand, clean up" loop with a live table: every local branch's origin status,
linked pull request, worktree, age, and deletion-risk verdict, plus sync,
cleanup, create, and worktree actions without leaving the screen.

## Install

Requires [`kk`](https://github.com/localshred/koolkit) (the koolkit plugin
manager). Once `kk-branches` is registered with it:

```
kk install branches
```

## Usage

Run with no arguments in any worktree of a repository to open the table:

```
kk branches
```

```
example-repo  trunk: main  ↻ fetched 3m ago

  BRANCH          ORIGIN      PR          WORKTREE            AGE
★ main            =           trunk       (main checkout)     2h
○ alice/fix       local only  — none      repo-wt-alice-fix   1d   confirm
○ feature/x       ↑2          #42 open    repo-wt-feature-x   3d   confirm

0 selected · space select · a select safe · d delete · w +worktree
```

Pass `--print` to print the table once and exit, without the interactive
UI — useful for scripts, piping, or a quick look:

```
kk branches --print
```

See [docs/usage.md](docs/usage.md) for every key, the risk levels and why a
branch gets one, how sync differs from a plain rebase loop, the worktree
path rule, and the `kk.trunks` / `kk.branchPrefix` / `kk.releasePattern`
config.

## Requirements

`kk-branches` declares the following runtime dependencies (checked by `kk
doctor`):

- `git`
- [`gh`](https://cli.github.com) — the GitHub CLI, used to look up pull
  requests. Without it, or without `gh auth login`, the PR column shows `?`
  and everything else still works.
