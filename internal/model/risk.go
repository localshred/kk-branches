package model

import "fmt"

// Level categorizes how safe a branch is to delete.
type Level int

const (
	// LevelNone means the branch is not a deletion candidate (it's a trunk).
	LevelNone Level = iota
	// LevelPending means the branch's pull-request lookup hasn't finished.
	LevelPending
	// LevelSafe means the branch can be deleted without confirmation.
	LevelSafe
	// LevelConfirm means the branch can be deleted, but only after
	// confirming the collected reasons.
	LevelConfirm
	// LevelBlocked means the branch cannot be deleted right now.
	LevelBlocked
)

// Risk is the outcome of assessing whether a branch is safe to delete.
type Risk struct {
	Level   Level
	Reasons []string
}

// Assess determines the deletion risk for branch b. Trunk branches are
// never risky; branches checked out in a worktree are blocked; branches
// with a pending PR lookup are pending; branches with a merged PR whose
// commits are fully upstreamed are safe. Everything else requires
// confirmation, with every applicable reason collected.
func Assess(b Branch) Risk {
	if b.Trunk {
		return Risk{Level: LevelNone}
	}
	if w := b.Worktree; w != nil {
		switch {
		case w.Main:
			return Risk{Level: LevelBlocked, Reasons: []string{"checked out in main checkout"}}
		case w.Current:
			return Risk{Level: LevelBlocked, Reasons: []string{"checked out in this worktree"}}
		case w.Dirty:
			return Risk{Level: LevelBlocked, Reasons: []string{"worktree has uncommitted changes"}}
		}
	}
	if b.Lookup == PRPending {
		return Risk{Level: LevelPending}
	}
	if b.Lookup == PRFound && b.PR != nil && b.PR.State == "MERGED" && b.InPR &&
		(b.Upstream == nil || b.Upstream.Gone || b.Upstream.Ahead == 0) {
		return Risk{Level: LevelSafe}
	}
	return Risk{Level: LevelConfirm, Reasons: confirmReasons(b)}
}

// confirmReasons collects every reason a branch requires confirmation
// before deletion, in the order the brief prescribes.
func confirmReasons(b Branch) []string {
	var reasons []string
	if b.Upstream == nil {
		reasons = append(reasons, "never pushed")
	}
	switch b.Lookup {
	case PRUnknown:
		reasons = append(reasons, "PR status unknown")
	case PRNone:
		reasons = append(reasons, "no PR found")
	case PRFound:
		if b.PR != nil {
			switch b.PR.State {
			case "OPEN":
				reasons = append(reasons, fmt.Sprintf("PR #%d still open", b.PR.Number))
			case "CLOSED":
				reasons = append(reasons, fmt.Sprintf("PR #%d closed without merge", b.PR.Number))
			case "MERGED":
				if !b.InPR {
					reasons = append(reasons, fmt.Sprintf("PR #%d merged but local commits not in it", b.PR.Number))
				}
			}
		}
	}
	if b.Upstream != nil && !b.Upstream.Gone && b.Upstream.Ahead > 0 {
		reasons = append(reasons, fmt.Sprintf("%d commits not on origin", b.Upstream.Ahead))
	}
	return reasons
}
