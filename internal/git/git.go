// Package git adapts koolkit's run.Runner to drive the git(1) CLI for
// kk-branches: opening a repository, running git subcommands against a
// worktree, and deriving the branch, worktree, and config state consumed
// by the rest of the plugin.
package git

import (
	"errors"
	"fmt"
	"strings"

	"github.com/localshred/koolkit/pkg/run"
)

// Repo is a git repository rooted at Dir, driven through Run.
type Repo struct {
	Run run.Runner
	Dir string
}

// Git runs a git subcommand in the repository's directory.
func (r Repo) Git(args ...string) (run.Result, error) {
	return r.GitIn(r.Dir, args...)
}

// GitIn runs a git subcommand with -C set to dir, which may be another
// worktree of the same repository.
func (r Repo) GitIn(dir string, args ...string) (run.Result, error) {
	full := append([]string{"-C", dir}, args...)
	res, err := r.Run.Run("git", full...)
	if err != nil {
		stderr := strings.TrimSpace(res.Stderr)
		if stderr != "" {
			return res, fmt.Errorf("%w: %s", err, stderr)
		}
		return res, err
	}
	return res, nil
}

// Open resolves dir to its repository toplevel and returns a Repo rooted
// there. It fails with "not a git repository" if dir is not inside a git
// working tree.
func Open(r run.Runner, dir string) (Repo, error) {
	res, err := r.Run("git", "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repo{}, errors.New("not a git repository")
	}
	return Repo{Run: r, Dir: strings.TrimSpace(res.Stdout)}, nil
}
