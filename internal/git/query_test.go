package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/koolkit/pkg/run"
)

func TestTrunkNamesDefaultsPlusConfig(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{Results: map[string]run.Result{
		"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": {Stdout: "origin/develop\n"},
		"git -C /repo config --get-all kk.trunks":                    {Stdout: "release qa\n"},
	}}}

	names := repo.TrunkNames()
	want := map[string]bool{"main": true, "master": true, "develop": true, "release": true, "qa": true}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want 5 unique entries", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected trunk name %q", n)
		}
	}
}

func TestTrunkNamesNoOriginHead(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{Errs: map[string]error{
		"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": errors.New("fatal: not found"),
		"git -C /repo config --get-all kk.trunks":                    errors.New("exit status 1"),
	}}}

	names := repo.TrunkNames()
	if len(names) != 2 || names[0] != "main" || names[1] != "master" {
		t.Fatalf("names = %v, want [main master]", names)
	}
}

func TestBranchPrefixFromConfig(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{Results: map[string]run.Result{
		"git -C /repo config kk.branchPrefix": {Stdout: "alice/\n"},
	}}}

	if got := repo.BranchPrefix(nil); got != "alice/" {
		t.Errorf("BranchPrefix() = %q, want alice/", got)
	}
}

func TestBranchPrefixFromAuthoredBranches(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{
		Errs: map[string]error{
			"git -C /repo config kk.branchPrefix": errors.New("exit status 1"),
		},
		Results: map[string]run.Result{
			"git -C /repo config user.email": {Stdout: "a@x\n"},
		},
	}}

	branches := []model.Branch{
		{Name: "alice/one", AuthorEmail: "a@x"},
		{Name: "alice/two", AuthorEmail: "a@x"},
		{Name: "bob/x", AuthorEmail: "b@x"},
		{Name: "spike", AuthorEmail: "a@x"},
	}

	if got := repo.BranchPrefix(branches); got != "alice/" {
		t.Errorf("BranchPrefix() = %q, want alice/", got)
	}
}

func TestBranchPrefixNone(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{
		Errs: map[string]error{
			"git -C /repo config kk.branchPrefix": errors.New("exit status 1"),
		},
		Results: map[string]run.Result{
			"git -C /repo config user.email": {Stdout: "a@x\n"},
		},
	}}

	branches := []model.Branch{{Name: "spike", AuthorEmail: "a@x"}}
	if got := repo.BranchPrefix(branches); got != "" {
		t.Errorf("BranchPrefix() = %q, want empty", got)
	}
}

func TestIsAncestorErrorIsFalse(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{Errs: map[string]error{
		"git -C /repo merge-base --is-ancestor a b": errors.New("exit status 1"),
	}}}

	if repo.IsAncestor("a", "b") {
		t.Error("IsAncestor() = true, want false on error")
	}
}

func TestReleaseBasesDefaultPatterns(t *testing.T) {
	repo := Repo{Dir: "/repo", Run: &run.Fake{
		Results: map[string]run.Result{
			"git -C /repo for-each-ref --sort=-version:refname --count=5 --format=%(refname:short) refs/remotes/origin/release* refs/remotes/origin/release*/**": {
				Stdout: "origin/release/2.0\norigin/release-1.10\n",
			},
		},
		Errs: map[string]error{
			"git -C /repo config --get-all kk.releasePattern": errors.New("exit status 1"),
		},
	}}

	got := repo.ReleaseBases(5)
	if len(got) != 2 || got[0] != "origin/release/2.0" || got[1] != "origin/release-1.10" {
		t.Fatalf("ReleaseBases = %v, want [origin/release/2.0 origin/release-1.10]", got)
	}
}

func TestReleaseBasesConfiguredPatterns(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /repo config --get-all kk.releasePattern": {Stdout: "hotfix/* stable-*\nlts\n"},
		"git -C /repo for-each-ref --sort=-version:refname --count=3 --format=%(refname:short) refs/remotes/origin/hotfix/* refs/remotes/origin/stable-* refs/remotes/origin/lts": {
			Stdout: "origin/stable-4\n",
		},
	}}
	repo := Repo{Dir: "/repo", Run: fake}

	got := repo.ReleaseBases(3)
	if len(got) != 1 || got[0] != "origin/stable-4" {
		t.Fatalf("ReleaseBases = %v, want [origin/stable-4]", got)
	}
	for _, call := range fake.Calls {
		for _, arg := range call {
			if strings.HasPrefix(arg, "refs/remotes/origin/release") {
				t.Fatalf("configured patterns must replace the defaults, got call %v", call)
			}
		}
	}
}
