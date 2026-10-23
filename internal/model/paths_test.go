package model

import (
	"testing"
	"time"
)

func TestWorktreePath(t *testing.T) {
	got := WorktreePath("/src/app", "alice/fix-login")
	want := "/src/app-wt-alice-fix-login"
	if got != want {
		t.Errorf("WorktreePath = %q, want %q", got, want)
	}
}

func TestSortTrunksFirstThenRecent(t *testing.T) {
	now := time.Now()
	bs := []Branch{
		{Name: "alice/old", CommitAt: now.Add(-48 * time.Hour)},
		{Name: "master", Trunk: true},
		{Name: "bob/new", CommitAt: now.Add(-1 * time.Hour)},
		{Name: "main", Trunk: true},
	}

	Sort(bs)

	want := []string{"main", "master", "bob/new", "alice/old"}
	for i, b := range bs {
		if b.Name != want[i] {
			t.Fatalf("Sort()[%d] = %q, want %q (full order %v)", i, b.Name, want[i], namesOf(bs))
		}
	}
}

func TestSortTiesBreakByName(t *testing.T) {
	now := time.Now()
	bs := []Branch{
		{Name: "zed/branch", CommitAt: now},
		{Name: "alice/branch", CommitAt: now},
	}

	Sort(bs)

	if bs[0].Name != "alice/branch" || bs[1].Name != "zed/branch" {
		t.Errorf("Sort() = %v, want alphabetical tiebreak", namesOf(bs))
	}
}

func namesOf(bs []Branch) []string {
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names
}
