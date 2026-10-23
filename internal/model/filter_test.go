package model

import (
	"testing"
	"time"
)

func TestFilterPresets(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	branches := []Branch{
		{Name: "main", Trunk: true, Upstream: &Upstream{}, CommitAt: now},
		{Name: "alice/merged", Lookup: PRFound, PR: &PR{Number: 1, State: "MERGED"}, Upstream: &Upstream{}, CommitAt: now},
		{Name: "alice/gone", Upstream: &Upstream{Gone: true}, CommitAt: now},
		{Name: "alice/local-only", CommitAt: now},
		{Name: "alice/worktree", Worktree: &Worktree{}, Upstream: &Upstream{}, CommitAt: now},
		{Name: "alice/dirty", Worktree: &Worktree{Dirty: true}, Upstream: &Upstream{}, CommitAt: now},
		{Name: "alice/stale", Upstream: &Upstream{}, CommitAt: now.Add(-40 * 24 * time.Hour)},
		{Name: "alice/fresh", Upstream: &Upstream{}, CommitAt: now.Add(-1 * time.Hour)},
	}

	tests := []struct {
		preset Preset
		want   []string
	}{
		{PresetAll, []string{"main", "alice/merged", "alice/gone", "alice/local-only", "alice/worktree", "alice/dirty", "alice/stale", "alice/fresh"}},
		{PresetMerged, []string{"alice/merged"}},
		{PresetGone, []string{"alice/gone"}},
		{PresetLocalOnly, []string{"alice/local-only"}},
		{PresetWorktree, []string{"alice/worktree", "alice/dirty"}},
		{PresetDirty, []string{"alice/dirty"}},
		{PresetStale, []string{"alice/stale"}},
	}

	for _, tt := range tests {
		t.Run(tt.preset.Label(), func(t *testing.T) {
			got := Filter(branches, tt.preset, "", now)
			names := make([]string, len(got))
			for i, b := range got {
				names[i] = b.Name
			}
			if len(names) != len(tt.want) {
				t.Fatalf("Filter(%s) = %v, want %v", tt.preset.Label(), names, tt.want)
			}
			for i := range names {
				if names[i] != tt.want[i] {
					t.Errorf("Filter(%s)[%d] = %q, want %q", tt.preset.Label(), i, names[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilterText(t *testing.T) {
	now := time.Now()
	branches := []Branch{
		{Name: "alice/fix-login"},
		{Name: "bob/add-feature"},
	}

	got := Filter(branches, PresetAll, "FIX", now)

	if len(got) != 1 || got[0].Name != "alice/fix-login" {
		t.Errorf("Filter text = %v, want only alice/fix-login", got)
	}
}

func TestPresetLabels(t *testing.T) {
	want := map[Preset]string{
		PresetAll:       "all",
		PresetMerged:    "merged",
		PresetGone:      "gone",
		PresetLocalOnly: "local-only",
		PresetWorktree:  "worktree",
		PresetDirty:     "dirty",
		PresetStale:     "stale",
	}
	for p, label := range want {
		if got := p.Label(); got != label {
			t.Errorf("Preset(%d).Label() = %q, want %q", p, got, label)
		}
	}
	if len(Presets) != len(want) {
		t.Errorf("len(Presets) = %d, want %d", len(Presets), len(want))
	}
}
