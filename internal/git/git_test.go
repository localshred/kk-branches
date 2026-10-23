package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/localshred/koolkit/pkg/run"
)

// stubRunner returns a fixed Result and error together, which run.Fake
// cannot do, to exercise GitIn's stderr-extraction path on a real
// subprocess failure.
type stubRunner struct {
	res run.Result
	err error
}

func (s stubRunner) Run(name string, args ...string) (run.Result, error) {
	return s.res, s.err
}

func (s stubRunner) Look(name string) (string, bool) {
	return "", false
}

func TestOpenSuccess(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /some/dir rev-parse --show-toplevel": {Stdout: "/repo\n"},
	}}

	repo, err := Open(fake, "/some/dir")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if repo.Dir != "/repo" {
		t.Errorf("repo.Dir = %q, want /repo", repo.Dir)
	}
}

func TestOpenNotGitRepository(t *testing.T) {
	fake := &run.Fake{Errs: map[string]error{
		"git -C /some/dir rev-parse --show-toplevel": errors.New("exit status 128"),
	}}

	_, err := Open(fake, "/some/dir")
	if err == nil || err.Error() != "not a git repository" {
		t.Fatalf("Open() error = %v, want \"not a git repository\"", err)
	}
}

func TestGitRunsInRepoDir(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /repo status --porcelain": {Stdout: ""},
	}}
	repo := Repo{Dir: "/repo", Run: fake}

	if _, err := repo.Git("status", "--porcelain"); err != nil {
		t.Fatalf("Git() error = %v", err)
	}
}

func TestGitInRunsInGivenDir(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /other status --porcelain": {Stdout: "M file\n"},
	}}
	repo := Repo{Dir: "/repo", Run: fake}

	res, err := repo.GitIn("/other", "status", "--porcelain")
	if err != nil {
		t.Fatalf("GitIn() error = %v", err)
	}
	if res.Stdout != "M file\n" {
		t.Errorf("res.Stdout = %q, want M file", res.Stdout)
	}
}

func TestGitNonZeroExitIncludesStderr(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: stubRunner{
		res: run.Result{Stderr: "  fatal: boom  \n"},
		err: errors.New("exit status 1"),
	}}

	_, err := repo.Git("status")
	if err == nil {
		t.Fatal("Git() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "fatal: boom") {
		t.Errorf("Git() error = %q, want it to contain trimmed stderr", err.Error())
	}
	if strings.Contains(err.Error(), "  fatal: boom  ") {
		t.Errorf("Git() error = %q, want stderr trimmed", err.Error())
	}
}
