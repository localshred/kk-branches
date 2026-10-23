// Package app implements kk-branches' application layer: loading the
// repository snapshot from git and GitHub, and the user actions on
// top of it — creating branches, adding and removing worktrees,
// deleting branches, and opening URLs. Deletion and worktree removal
// can lose a user's work, so their safety checks live here rather than
// in the TUI.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/localshred/kk-branches/internal/gh"
	"github.com/localshred/kk-branches/internal/git"
	"github.com/localshred/kk-branches/internal/model"
)

// App is the application layer, holding the adapters and clock that
// every action is driven through.
type App struct {
	Repo git.Repo
	GH   gh.Client
	Now  func() time.Time
}

// AddWorktree creates a worktree for branch at the conventional path
// alongside the main checkout in s, refusing when branch is already
// checked out somewhere.
func (a App) AddWorktree(s model.Snapshot, branch string) (string, error) {
	for _, b := range s.Branches {
		if b.Name == branch && b.Worktree != nil {
			return "", fmt.Errorf("branch %s already checked out at %s", branch, b.Worktree.Path)
		}
	}
	if s.MainPath == "" {
		return "", errors.New("no main worktree found")
	}
	return a.addWorktree(s.MainPath, branch)
}

// Bases returns the branch-creation base candidates: the default
// branch (from s.Trunk, falling back to "main") qualified under
// origin, followed by the five most recent release bases.
func (a App) Bases(s model.Snapshot) []string {
	trunk := s.Trunk
	if trunk == "" {
		trunk = "main"
	}
	bases := []string{"origin/" + trunk}
	return append(bases, a.Repo.ReleaseBases(5)...)
}

// Create validates and creates a new local branch from req, then
// performs req.Then: checking it out, adding a worktree for it, or
// nothing more.
func (a App) Create(s model.Snapshot, req CreateRequest) (worktreePath string, err error) {
	if _, err := a.Repo.Git("check-ref-format", "--branch", req.Name); err != nil {
		return "", err
	}
	for _, b := range s.Branches {
		if b.Name == req.Name {
			return "", fmt.Errorf("branch %s already exists", req.Name)
		}
	}
	if _, err := a.Repo.Git("branch", "--no-track", req.Name, req.Base); err != nil {
		return "", err
	}

	switch req.Then {
	case ThenCheckout:
		if _, err := a.Repo.Git("checkout", req.Name); err != nil {
			return "", err
		}
		return "", nil
	case ThenWorktree:
		if s.MainPath == "" {
			return "", errors.New("no main worktree found")
		}
		return a.addWorktree(s.MainPath, req.Name)
	default:
		return "", nil
	}
}

// CreateRequest describes a new branch to create and what to do with
// it once it exists.
type CreateRequest struct {
	Name string
	Base string
	Then Then
}

// Delete removes branch b, refusing unless model.Assess(b) is Safe or
// Confirm. When b has a worktree, its dirtiness is rechecked
// immediately before removal, since the cached state in b may be
// stale.
func (a App) Delete(b model.Branch) error {
	risk := model.Assess(b)
	if risk.Level != model.LevelSafe && risk.Level != model.LevelConfirm {
		if len(risk.Reasons) == 0 {
			return fmt.Errorf("refusing to delete branch %s", b.Name)
		}
		return fmt.Errorf("refusing to delete branch %s: %s", b.Name, strings.Join(risk.Reasons, "; "))
	}

	if b.Worktree != nil {
		dirty, err := a.Repo.IsDirty(b.Worktree.Path)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("worktree became dirty: %s", b.Worktree.Path)
		}
		if _, err := a.Repo.Git("worktree", "remove", b.Worktree.Path); err != nil {
			return err
		}
	}

	_, err := a.Repo.Git("branch", "-D", b.Name)
	return err
}

// Load builds a fresh model.Snapshot from the repository: every local
// branch joined with its worktree (if any) and trunk status, sorted
// for display.
func (a App) Load() (model.Snapshot, error) {
	branches, err := a.Repo.Branches()
	if err != nil {
		return model.Snapshot{}, err
	}
	worktrees, err := a.Repo.Worktrees()
	if err != nil {
		return model.Snapshot{}, err
	}

	byBranch := make(map[string]model.Worktree, len(worktrees))
	mainPath := ""
	for _, w := range worktrees {
		if w.Branch != "" {
			byBranch[w.Branch] = w
		}
		if w.Main {
			mainPath = w.Path
		}
	}

	trunks := make(map[string]bool)
	for _, name := range a.Repo.TrunkNames() {
		trunks[name] = true
	}

	for i := range branches {
		branches[i].Trunk = trunks[branches[i].Name]
		if w, ok := byBranch[branches[i].Name]; ok {
			wCopy := w
			branches[i].Worktree = &wCopy
		}
		if !branches[i].Trunk {
			branches[i].Lookup = model.PRPending
		}
	}

	model.Sort(branches)

	return model.Snapshot{
		Branches:  branches,
		FetchedAt: a.Repo.FetchedAt(),
		MainPath:  mainPath,
		Prefix:    a.Repo.BranchPrefix(branches),
		Repo:      filepath.Base(mainPath),
		Trunk:     a.Repo.DefaultBranch(),
	}, nil
}

// LoadPRs resolves the pull request for every non-trunk branch in s
// and returns the updated snapshot, leaving s itself untouched. If
// GH.Repo or GH.PRsByHead fails, every non-trunk branch in the
// returned snapshot is marked PRUnknown and the error is returned
// alongside it.
func (a App) LoadPRs(s model.Snapshot) (model.Snapshot, error) {
	s.Branches = append([]model.Branch(nil), s.Branches...)

	owner, name, err := a.GH.Repo()
	if err != nil {
		markUnknown(s.Branches)
		return s, err
	}

	heads := make([]string, 0, len(s.Branches))
	for _, b := range s.Branches {
		if !b.Trunk {
			heads = append(heads, headFor(b))
		}
	}

	prs, err := a.GH.PRsByHead(owner, name, heads)
	if err != nil {
		markUnknown(s.Branches)
		return s, err
	}

	for i := range s.Branches {
		b := &s.Branches[i]
		if b.Trunk {
			continue
		}
		pr, ok := prs[headFor(*b)]
		if !ok {
			b.PR = nil
			b.Lookup = model.PRNone
			b.InPR = false
			continue
		}
		b.PR = &pr
		b.Lookup = model.PRFound
		b.InPR = b.Tip == pr.HeadOID || a.Repo.IsAncestor(b.Tip, pr.HeadOID)
	}

	return s, nil
}

// Open opens url in the user's default browser: `open` on darwin,
// `xdg-open` elsewhere.
func (a App) Open(url string) error {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_, err := a.Repo.Run.Run(cmd, url)
	return err
}

// RemoveWorktree removes b's worktree, refusing when it is the main
// checkout, the worktree kk is running from, or freshly dirty. It
// never passes --force.
func (a App) RemoveWorktree(b model.Branch) error {
	w := b.Worktree
	if w == nil {
		return fmt.Errorf("branch %s has no worktree", b.Name)
	}
	if w.Main {
		return fmt.Errorf("refusing to remove the main worktree: %s", w.Path)
	}
	if w.Current {
		return fmt.Errorf("refusing to remove the current worktree: %s", w.Path)
	}

	dirty, err := a.Repo.IsDirty(w.Path)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("worktree has uncommitted changes: %s", w.Path)
	}

	_, err = a.Repo.Git("worktree", "remove", w.Path)
	return err
}

// Then names what Create does once a new branch exists.
type Then int

const (
	// ThenCheckout checks the new branch out in the main checkout.
	ThenCheckout Then = iota
	// ThenWorktree adds a worktree for the new branch.
	ThenWorktree
	// ThenCreate does nothing more; the branch is left unchecked out.
	ThenCreate
)

// addWorktree creates a worktree for branch at the conventional path
// alongside mainPath, refusing when that path already exists.
func (a App) addWorktree(mainPath, branch string) (string, error) {
	path := model.WorktreePath(mainPath, branch)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if _, err := a.Repo.Git("worktree", "add", path, branch); err != nil {
		return "", err
	}
	return path, nil
}

// headFor returns the head branch name GitHub pull requests are
// looked up by: b's upstream head when it has one, else b's own name.
func headFor(b model.Branch) string {
	if b.Upstream != nil {
		return b.Upstream.Head
	}
	return b.Name
}

// markUnknown sets Lookup to PRUnknown on every non-trunk branch in
// bs, in place.
func markUnknown(bs []model.Branch) {
	for i := range bs {
		if !bs[i].Trunk {
			bs[i].Lookup = model.PRUnknown
		}
	}
}
