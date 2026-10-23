package cmd

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/localshred/koolkit/pkg/run"
)

// hybridRunner delegates every call that isn't "gh" to the real
// run.Default runner, so a test can exercise an actual temp git
// repository, while always failing "gh" calls so the test never
// shells out to the real gh CLI.
type hybridRunner struct{}

func (hybridRunner) Run(name string, args ...string) (run.Result, error) {
	if name == "gh" {
		return run.Result{}, errors.New("gh not available in tests")
	}
	return run.Default.Run(name, args...)
}

func (hybridRunner) Look(name string) (string, bool) {
	if name == "gh" {
		return "", false
	}
	return run.Default.Look(name)
}

func TestNewRootUse(t *testing.T) {
	root := NewRoot()
	if root.Use != "branches" {
		t.Fatalf("Use = %q, want %q", root.Use, "branches")
	}
}

func TestPrintFlagRendersTable(t *testing.T) {
	dir := initTempRepo(t)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	root := newRoot(hybridRunner{})
	root.SetArgs([]string{"--print"})

	output := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	})

	if !strings.Contains(output, "alice/fix-login") {
		t.Errorf("output missing %q:\n%s", "alice/fix-login", output)
	}
	if !strings.Contains(output, "feature/widgets") {
		t.Errorf("output missing %q:\n%s", "feature/widgets", output)
	}
	if !strings.Contains(output, "local only") {
		t.Errorf("output missing %q:\n%s", "local only", output)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(out)
}

// initTempRepo creates a throwaway git repository with two commits on
// main and two branches, one of them ("feature/widgets") with its own
// commit, and no remote.
func initTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "initial commit")
	git("branch", "alice/fix-login")
	git("checkout", "-b", "feature/widgets")
	git("commit", "--allow-empty", "-m", "widgets")
	git("checkout", "main")

	return dir
}
