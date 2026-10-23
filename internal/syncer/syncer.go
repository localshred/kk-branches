// Package syncer reproduces the user's reposync shell workflow as a
// library: fetch origin, autostash local changes, rebase every
// tracked local branch onto its upstream, then restore the starting
// HEAD and pop the autostash. Rebases and stashes touch the user's
// real work, so every per-branch step that would conflict or require
// touching a dirty worktree skips or aborts instead of forcing
// through.
package syncer

import (
	"strings"

	"github.com/localshred/kk-branches/internal/git"
	"github.com/localshred/kk-branches/internal/model"
)

// Engine runs a sync over Repo.
type Engine struct{ Repo git.Repo }

// Event reports one step of a sync run, suitable for streaming to a
// progress display.
type Event struct {
	// Branch is the branch name the event concerns, or "" for
	// whole-run events such as StatusFetching and StatusDone.
	Branch string
	Status Status
	// Detail is a short human-readable elaboration, e.g. "+3",
	// "worktree dirty", "rebase conflict, aborted", or error text.
	Detail string
	// Done is the number of candidate branches finished so far.
	Done int
	// Total is the number of candidate branches being synced.
	Total int
}

// Run fetches origin, autostashes any local changes, rebases every
// branch in branches that has a live upstream onto that upstream, then
// restores the starting HEAD and pops the autostash. It emits one or
// more Events per step via emit and returns an error only when a step
// the rest of the run depends on — the fetch or the autostash — fails;
// per-branch and stash-pop failures are reported through emit instead
// so the run can finish and restore the user's HEAD.
func (e Engine) Run(branches []model.Branch, emit func(Event)) error {
	emit(Event{Status: StatusFetching})
	if _, err := e.Repo.Git("fetch", "origin", "--prune"); err != nil {
		emit(Event{Status: StatusFailed, Detail: err.Error()})
		return err
	}

	start, err := e.currentRef()
	if err != nil {
		emit(Event{Status: StatusFailed, Detail: err.Error()})
		return err
	}

	dirty, err := e.Repo.IsDirty(e.Repo.Dir)
	if err != nil {
		emit(Event{Status: StatusFailed, Detail: err.Error()})
		return err
	}
	stashed := false
	if dirty {
		if _, err := e.Repo.Git("stash", "push", "--include-untracked", "--quiet", "-m", "kk branches autostash"); err != nil {
			emit(Event{Status: StatusFailed, Detail: err.Error()})
			return err
		}
		stashed = true
	}

	candidates := make([]model.Branch, 0, len(branches))
	for _, b := range branches {
		if b.Upstream != nil && !b.Upstream.Gone {
			candidates = append(candidates, b)
		}
	}
	total := len(candidates)

	headMoved := false
	for i, b := range candidates {
		emit(Event{Branch: b.Name, Status: StatusRunning, Done: i, Total: total})
		moved, ev := e.syncBranch(b)
		headMoved = headMoved || moved
		ev.Done, ev.Total = i+1, total
		emit(ev)
	}

	if headMoved {
		if _, err := e.Repo.Git("checkout", "--quiet", start); err != nil {
			emit(Event{Status: StatusFailed, Detail: err.Error()})
		}
	}

	if stashed {
		if _, err := e.Repo.Git("stash", "pop", "--quiet"); err != nil {
			emit(Event{Status: StatusStashConflict, Detail: "stash pop conflicted; resolve manually (git stash list)"})
		}
	}

	emit(Event{Status: StatusDone, Done: total, Total: total})
	return nil
}

// Status is the outcome or progress stage of one Event emitted by
// Engine.Run.
type Status int

const (
	// StatusFetching marks the initial `git fetch origin --prune`.
	StatusFetching Status = iota
	// StatusRunning marks the start of processing one candidate branch.
	StatusRunning
	// StatusRebased means the branch was rebased onto, or fast-forwarded
	// to, its upstream.
	StatusRebased
	// StatusUpToDate means the branch's tip already matches its
	// upstream.
	StatusUpToDate
	// StatusSkipped means the branch was left alone, e.g. because its
	// worktree was dirty.
	StatusSkipped
	// StatusConflict means a rebase failed and was aborted.
	StatusConflict
	// StatusStashConflict means popping the autostash conflicted; the
	// stash was left in place for manual resolution.
	StatusStashConflict
	// StatusDone marks the end of a run.
	StatusDone
	// StatusFailed means a step the rest of the run depends on — the
	// fetch or the autostash — failed.
	StatusFailed
)

// currentRef returns the branch name HEAD points to, or its raw SHA
// when HEAD is detached.
func (e Engine) currentRef() (string, error) {
	res, err := e.Repo.Git("symbolic-ref", "--short", "-q", "HEAD")
	if ref := strings.TrimSpace(res.Stdout); err == nil && ref != "" {
		return ref, nil
	}
	res, err = e.Repo.Git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// syncBranch brings one candidate branch up to date with its upstream
// and reports whether it moved HEAD in Repo.Dir.
func (e Engine) syncBranch(b model.Branch) (bool, Event) {
	localRes, err := e.Repo.Git("rev-parse", b.Name)
	if err != nil {
		return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
	}
	local := strings.TrimSpace(localRes.Stdout)

	upstreamRes, err := e.Repo.Git("rev-parse", b.Upstream.Ref)
	if err != nil {
		return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
	}
	upstream := strings.TrimSpace(upstreamRes.Stdout)

	if local == upstream {
		return false, Event{Branch: b.Name, Status: StatusUpToDate}
	}

	countRes, err := e.Repo.Git("rev-list", "--count", b.Name+".."+b.Upstream.Ref)
	if err != nil {
		return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
	}
	detail := "+" + strings.TrimSpace(countRes.Stdout)

	switch {
	case b.Worktree != nil && !b.Worktree.Current:
		return e.syncOtherWorktree(b, detail)
	case b.Worktree == nil && e.Repo.IsAncestor(b.Name, b.Upstream.Ref):
		if _, err := e.Repo.Git("update-ref", "refs/heads/"+b.Name, upstream, local); err != nil {
			return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
		}
		return false, Event{Branch: b.Name, Status: StatusRebased, Detail: detail}
	default:
		return e.syncHere(b, detail)
	}
}

// syncHere rebases b onto its upstream directly in Repo.Dir, via
// checkout. It handles the branch currently checked out in Repo.Dir
// and local-only branches that have diverged from their upstream.
func (e Engine) syncHere(b model.Branch, detail string) (bool, Event) {
	if _, err := e.Repo.Git("checkout", "--quiet", b.Name); err != nil {
		return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
	}
	if _, err := e.Repo.Git("rebase", b.Upstream.Ref, "--quiet"); err != nil {
		e.Repo.Git("rebase", "--abort")
		return true, Event{Branch: b.Name, Status: StatusConflict, Detail: "rebase conflict, aborted"}
	}
	return true, Event{Branch: b.Name, Status: StatusRebased, Detail: detail}
}

// syncOtherWorktree rebases b in its own worktree, skipping it when
// that worktree is dirty rather than touching the user's uncommitted
// work.
func (e Engine) syncOtherWorktree(b model.Branch, detail string) (bool, Event) {
	path := b.Worktree.Path
	dirty, err := e.Repo.IsDirty(path)
	if err != nil {
		return false, Event{Branch: b.Name, Status: StatusFailed, Detail: err.Error()}
	}
	if dirty {
		return false, Event{Branch: b.Name, Status: StatusSkipped, Detail: "worktree dirty"}
	}
	if _, err := e.Repo.GitIn(path, "rebase", b.Upstream.Ref, "--quiet"); err != nil {
		e.Repo.GitIn(path, "rebase", "--abort")
		return false, Event{Branch: b.Name, Status: StatusConflict, Detail: "rebase conflict, aborted"}
	}
	return false, Event{Branch: b.Name, Status: StatusRebased, Detail: detail}
}
