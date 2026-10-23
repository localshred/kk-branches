// Package model defines the pure data types shared across kk-branches:
// branches, worktrees, pull requests, and the snapshot of repository state
// that rendering, filtering, and the git adapter all operate on.
package model

import "time"

// Branch describes a local git branch and everything known about its
// upstream, worktree checkout, and pull request.
type Branch struct {
	Name        string
	Tip         string
	AuthorEmail string
	CommitAt    time.Time
	Upstream    *Upstream // nil = never pushed
	Trunk       bool
	Worktree    *Worktree // nil = not checked out anywhere
	PR          *PR
	Lookup      PRLookup
	InPR        bool // Tip == PR.HeadOID or Tip is an ancestor of it
}

// PR describes a GitHub pull request associated with a branch.
type PR struct {
	Number    int
	URL       string
	State     string // "OPEN" | "MERGED" | "CLOSED"
	Title     string
	HeadOID   string
	Checks    string // "passing" | "failing" | "running" | ""
	UpdatedAt time.Time
}

// PRLookup describes the state of a branch's pull-request lookup.
type PRLookup int

const (
	// PRPending means the lookup has not completed yet.
	PRPending PRLookup = iota
	// PRFound means a matching pull request was found.
	PRFound
	// PRNone means no pull request exists for the branch.
	PRNone
	// PRUnknown means the lookup failed or was not attempted.
	PRUnknown
)

// Snapshot is a point-in-time view of a repository's branches, ready for
// rendering.
type Snapshot struct {
	Repo      string    // basename of main checkout
	Trunk     string    // default branch name for the header ("" if unknown)
	FetchedAt time.Time // zero if never fetched
	Branches  []Branch
	Prefix    string // branch-name prefill, e.g. "alice/"
}

// Upstream describes a branch's remote-tracking ref and its divergence
// from the local branch.
type Upstream struct {
	Ref    string // "origin/alice/fix" (upstream:short)
	Head   string // remote branch name, "alice/fix" (upstream:remoteref minus refs/heads/)
	Gone   bool
	Ahead  int
	Behind int
}

// Worktree describes one entry from `git worktree list`.
type Worktree struct {
	Path    string
	Branch  string // "" when detached
	Head    string
	Main    bool // first entry of `git worktree list`
	Current bool // the worktree kk runs from
	Dirty   bool
}
