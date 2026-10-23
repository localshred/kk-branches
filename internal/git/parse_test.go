package git

import (
	"strings"
	"testing"
	"time"
)

func TestParseBranchesTrackAndGone(t *testing.T) {
	lines := []string{
		"main\x00aaa\x00<a@x>\x001700000000\x00origin/main\x00refs/heads/main\x00",
		"alice/fix\x00bbb\x00<a@x>\x001700000100\x00origin/alice/fix\x00refs/heads/alice/fix\x00ahead 2, behind 1",
		"bob/old\x00ccc\x00<b@x>\x001700000200\x00origin/bob/old\x00refs/heads/bob/old\x00gone",
		"spike\x00ddd\x00<a@x>\x001700000300\x00\x00\x00",
		"local-name\x00eee\x00<a@x>\x001700000400\x00origin/other/name\x00refs/heads/other/name\x00",
	}
	branches := ParseBranches(strings.Join(lines, "\n"))
	if len(branches) != 5 {
		t.Fatalf("len(branches) = %d, want 5", len(branches))
	}

	main := branches[0]
	if main.AuthorEmail != "a@x" {
		t.Errorf("main.AuthorEmail = %q, want a@x", main.AuthorEmail)
	}
	if !main.CommitAt.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("main.CommitAt = %v, want %v", main.CommitAt, time.Unix(1700000000, 0))
	}
	if main.Upstream == nil {
		t.Fatal("main.Upstream = nil, want non-nil")
	}
	if main.Upstream.Ref != "origin/main" || main.Upstream.Head != "main" {
		t.Errorf("main.Upstream = %+v, want Ref origin/main Head main", main.Upstream)
	}
	if main.Upstream.Ahead != 0 || main.Upstream.Behind != 0 {
		t.Errorf("main.Upstream ahead/behind = %d/%d, want 0/0", main.Upstream.Ahead, main.Upstream.Behind)
	}

	fix := branches[1]
	if fix.Upstream == nil || fix.Upstream.Ahead != 2 || fix.Upstream.Behind != 1 {
		t.Errorf("fix.Upstream = %+v, want Ahead 2 Behind 1", fix.Upstream)
	}

	old := branches[2]
	if old.Upstream == nil || !old.Upstream.Gone {
		t.Errorf("old.Upstream = %+v, want Gone true", old.Upstream)
	}

	spike := branches[3]
	if spike.Upstream != nil {
		t.Errorf("spike.Upstream = %+v, want nil", spike.Upstream)
	}

	localName := branches[4]
	if localName.Upstream == nil || localName.Upstream.Head != "other/name" {
		t.Errorf("localName.Upstream = %+v, want Head other/name", localName.Upstream)
	}
}

func TestParseWorktrees(t *testing.T) {
	out := strings.Join([]string{
		"worktree /r/app",
		"HEAD aaa",
		"branch refs/heads/main",
		"",
		"worktree /r/app-wt-alice-fix",
		"HEAD bbb",
		"branch refs/heads/alice/fix",
		"",
		"worktree /r/tmp",
		"HEAD ccc",
		"detached",
		"",
	}, "\n")

	worktrees := ParseWorktrees(out, "/r/app-wt-alice-fix")
	if len(worktrees) != 3 {
		t.Fatalf("len(worktrees) = %d, want 3", len(worktrees))
	}

	if !worktrees[0].Main {
		t.Errorf("worktrees[0].Main = false, want true")
	}
	if worktrees[0].Branch != "main" {
		t.Errorf("worktrees[0].Branch = %q, want main", worktrees[0].Branch)
	}

	if !worktrees[1].Current {
		t.Errorf("worktrees[1].Current = false, want true")
	}
	if worktrees[1].Branch != "alice/fix" {
		t.Errorf("worktrees[1].Branch = %q, want alice/fix", worktrees[1].Branch)
	}

	if worktrees[2].Branch != "" {
		t.Errorf("worktrees[2].Branch = %q, want empty for detached", worktrees[2].Branch)
	}
	if worktrees[2].Main || worktrees[2].Current {
		t.Errorf("worktrees[2] = %+v, want Main false Current false", worktrees[2])
	}
}

func TestParseWorktreesPrunableIsDirty(t *testing.T) {
	out := strings.Join([]string{
		"worktree /r/app",
		"HEAD aaa",
		"branch refs/heads/main",
		"",
		"worktree /r/app-wt-gone",
		"HEAD bbb",
		"branch refs/heads/alice/gone",
		"prunable gitdir file points to non-existent location",
		"",
	}, "\n")

	worktrees := ParseWorktrees(out, "/r/app")
	if len(worktrees) != 2 {
		t.Fatalf("len(worktrees) = %d, want 2", len(worktrees))
	}
	if worktrees[0].Dirty {
		t.Errorf("worktrees[0].Dirty = true, want false")
	}
	if !worktrees[1].Dirty {
		t.Errorf("worktrees[1].Dirty = false, want true for a prunable entry")
	}
}
