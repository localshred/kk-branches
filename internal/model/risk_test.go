package model

import (
	"reflect"
	"testing"
)

func TestAssess(t *testing.T) {
	tests := []struct {
		name        string
		branch      Branch
		wantLevel   Level
		wantReasons []string
	}{
		{
			name:      "trunk is never risky",
			branch:    Branch{Name: "main", Trunk: true},
			wantLevel: LevelNone,
		},
		{
			name:        "checked out in main checkout blocks",
			branch:      Branch{Name: "alice/fix", Worktree: &Worktree{Main: true}},
			wantLevel:   LevelBlocked,
			wantReasons: []string{"checked out in main checkout"},
		},
		{
			name:        "checked out in this worktree blocks",
			branch:      Branch{Name: "alice/fix", Worktree: &Worktree{Current: true}},
			wantLevel:   LevelBlocked,
			wantReasons: []string{"checked out in this worktree"},
		},
		{
			name:        "dirty worktree blocks",
			branch:      Branch{Name: "alice/fix", Worktree: &Worktree{Dirty: true}},
			wantLevel:   LevelBlocked,
			wantReasons: []string{"worktree has uncommitted changes"},
		},
		{
			name:      "pending PR lookup",
			branch:    Branch{Name: "alice/fix", Lookup: PRPending},
			wantLevel: LevelPending,
		},
		{
			name: "merged, in PR, never pushed is safe",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound, InPR: true,
				PR: &PR{Number: 1, State: "MERGED"},
			},
			wantLevel: LevelSafe,
		},
		{
			name: "merged, in PR, upstream gone is safe",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound, InPR: true,
				PR:       &PR{Number: 1, State: "MERGED"},
				Upstream: &Upstream{Gone: true},
			},
			wantLevel: LevelSafe,
		},
		{
			name: "merged, in PR, ahead zero is safe",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound, InPR: true,
				PR:       &PR{Number: 1, State: "MERGED"},
				Upstream: &Upstream{Ahead: 0},
			},
			wantLevel: LevelSafe,
		},
		{
			name: "merged, in PR, but still ahead needs confirm",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound, InPR: true,
				PR:       &PR{Number: 1, State: "MERGED"},
				Upstream: &Upstream{Ahead: 2},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"2 commits not on origin"},
		},
		{
			name:        "never pushed and unknown lookup",
			branch:      Branch{Name: "alice/fix", Lookup: PRUnknown},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"never pushed", "PR status unknown"},
		},
		{
			name: "no PR found",
			branch: Branch{
				Name: "alice/fix", Lookup: PRNone,
				Upstream: &Upstream{},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"no PR found"},
		},
		{
			name: "PR still open",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound,
				PR:       &PR{Number: 5, State: "OPEN"},
				Upstream: &Upstream{},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"PR #5 still open"},
		},
		{
			name: "PR closed without merge",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound,
				PR:       &PR{Number: 6, State: "CLOSED"},
				Upstream: &Upstream{},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"PR #6 closed without merge"},
		},
		{
			name: "PR merged but local commits not in it",
			branch: Branch{
				Name: "alice/fix", Lookup: PRFound, InPR: false,
				PR:       &PR{Number: 7, State: "MERGED"},
				Upstream: &Upstream{},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"PR #7 merged but local commits not in it"},
		},
		{
			name: "commits not on origin",
			branch: Branch{
				Name: "alice/fix", Lookup: PRNone,
				Upstream: &Upstream{Ahead: 3},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"no PR found", "3 commits not on origin"},
		},
		{
			name: "ahead but gone doesn't add commits reason",
			branch: Branch{
				Name: "alice/fix", Lookup: PRNone,
				Upstream: &Upstream{Ahead: 3, Gone: true},
			},
			wantLevel:   LevelConfirm,
			wantReasons: []string{"no PR found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assess(tt.branch)
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", got.Level, tt.wantLevel)
			}
			if !reflect.DeepEqual(got.Reasons, tt.wantReasons) {
				t.Errorf("Reasons = %v, want %v", got.Reasons, tt.wantReasons)
			}
		})
	}
}

func TestAssessBlocksCurrentWorktree(t *testing.T) {
	b := Branch{
		Name:     "alice/fix",
		Worktree: &Worktree{Current: true, Main: false, Dirty: false},
		Lookup:   PRFound,
		InPR:     true,
		PR:       &PR{Number: 1, State: "MERGED"},
	}

	got := Assess(b)

	if got.Level != LevelBlocked {
		t.Fatalf("Level = %v, want LevelBlocked", got.Level)
	}
	want := []string{"checked out in this worktree"}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Errorf("Reasons = %v, want %v", got.Reasons, want)
	}
}

func TestAssessUnknownPRNeverSafe(t *testing.T) {
	b := Branch{
		Name:     "alice/fix",
		Lookup:   PRUnknown,
		Upstream: &Upstream{Gone: true},
	}

	got := Assess(b)

	if got.Level != LevelConfirm {
		t.Fatalf("Level = %v, want LevelConfirm", got.Level)
	}
	found := false
	for _, r := range got.Reasons {
		if r == "PR status unknown" {
			found = true
		}
	}
	if !found {
		t.Errorf("Reasons = %v, want to contain %q", got.Reasons, "PR status unknown")
	}
}

func TestAssessMergedButNotInPR(t *testing.T) {
	b := Branch{
		Name:     "alice/fix",
		Lookup:   PRFound,
		InPR:     false,
		PR:       &PR{Number: 7, State: "MERGED"},
		Upstream: &Upstream{},
	}

	got := Assess(b)

	if got.Level != LevelConfirm {
		t.Fatalf("Level = %v, want LevelConfirm", got.Level)
	}
	found := false
	for _, r := range got.Reasons {
		if r == "PR #7 merged but local commits not in it" {
			found = true
		}
	}
	if !found {
		t.Errorf("Reasons = %v, want to contain %q", got.Reasons, "PR #7 merged but local commits not in it")
	}
}
