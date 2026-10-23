package git

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/localshred/kk-branches/internal/model"
)

// BranchPrefix returns the branch-name prefill for new branches: the
// kk.branchPrefix git config value if set, else the most frequent first
// path segment (plus "/") among bs whose AuthorEmail matches user.email,
// with ties broken alphabetically. It returns "" when neither yields a
// prefix.
func (r Repo) BranchPrefix(bs []model.Branch) string {
	if res, err := r.Git("config", "kk.branchPrefix"); err == nil {
		if prefix := strings.TrimSpace(res.Stdout); prefix != "" {
			return prefix
		}
	}

	emailRes, err := r.Git("config", "user.email")
	if err != nil {
		return ""
	}
	email := strings.TrimSpace(emailRes.Stdout)

	counts := map[string]int{}
	for _, b := range bs {
		if b.AuthorEmail != email {
			continue
		}
		idx := strings.Index(b.Name, "/")
		if idx <= 0 {
			continue
		}
		counts[b.Name[:idx+1]]++
	}
	if len(counts) == 0 {
		return ""
	}

	segments := make([]string, 0, len(counts))
	for segment := range counts {
		segments = append(segments, segment)
	}
	sort.Strings(segments)

	best, bestCount := "", -1
	for _, segment := range segments {
		if counts[segment] > bestCount {
			best, bestCount = segment, counts[segment]
		}
	}
	return best
}

// Branches returns every local branch and its parsed upstream state.
func (r Repo) Branches() ([]model.Branch, error) {
	format := "%(refname:short)%00%(objectname)%00%(authoremail)%00" +
		"%(committerdate:unix)%00%(upstream:short)%00%(upstream:remoteref)%00" +
		"%(upstream:track,nobracket)"
	res, err := r.Git("for-each-ref", "--format="+format, "refs/heads/")
	if err != nil {
		return nil, err
	}
	return ParseBranches(res.Stdout), nil
}

// DefaultBranch returns the repository's default branch name, derived
// from origin/HEAD, or "" if it cannot be determined.
func (r Repo) DefaultBranch() string {
	res, err := r.Git("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(res.Stdout), "origin/")
}

// FetchedAt returns the mtime of FETCH_HEAD in the repository's common
// git directory, or the zero time if it has never been fetched.
func (r Repo) FetchedAt() time.Time {
	res, err := r.Git("rev-parse", "--git-common-dir")
	if err != nil {
		return time.Time{}
	}
	gitDir := strings.TrimSpace(res.Stdout)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(r.Dir, gitDir)
	}
	info, err := os.Stat(filepath.Join(gitDir, "FETCH_HEAD"))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// IsAncestor reports whether a is an ancestor of b. Any error, including
// a is not an ancestor, is treated as false.
func (r Repo) IsAncestor(a, b string) bool {
	_, err := r.Git("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// IsDirty reports whether the worktree at path has uncommitted changes.
func (r Repo) IsDirty(path string) (bool, error) {
	res, err := r.GitIn(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) != "", nil
}

// ReleaseBases returns the n most recent origin/release-* branch names,
// newest first.
func (r Repo) ReleaseBases(n int) []string {
	res, err := r.Git(
		"for-each-ref",
		"--sort=-version:refname",
		fmt.Sprintf("--count=%d", n),
		"--format=%(refname:short)",
		"refs/remotes/origin/release-*",
	)
	if err != nil {
		return nil
	}
	return splitLines(res.Stdout)
}

// TrunkNames returns the repository's trunk branch names: the default
// branch plus "main" and "master", extended with any values from the
// multi-valued kk.trunks git config. Duplicates are removed.
func (r Repo) TrunkNames() []string {
	names := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	add(r.DefaultBranch())
	add("main")
	add("master")

	if res, err := r.Git("config", "--get-all", "kk.trunks"); err == nil {
		for _, line := range splitLines(res.Stdout) {
			for _, value := range strings.Fields(line) {
				add(value)
			}
		}
	}
	return names
}

// Worktrees returns every worktree of the repository, with Dirty filled
// in via IsDirty. A prunable worktree, or one whose status cannot be
// read, is marked Dirty rather than failing the whole listing.
func (r Repo) Worktrees() ([]model.Worktree, error) {
	res, err := r.Git("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	worktrees := ParseWorktrees(res.Stdout, r.Dir)
	for i := range worktrees {
		if worktrees[i].Dirty {
			continue
		}
		dirty, err := r.IsDirty(worktrees[i].Path)
		worktrees[i].Dirty = dirty || err != nil
	}
	return worktrees, nil
}

// splitLines splits s on newlines, trimming trailing carriage returns and
// dropping empty lines.
func splitLines(s string) []string {
	lines := []string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
