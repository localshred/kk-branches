package model

import (
	"strings"
	"time"
)

// Preset names a built-in branch list filter.
type Preset int

const (
	// PresetAll keeps every branch.
	PresetAll Preset = iota
	// PresetMerged keeps branches with a merged pull request.
	PresetMerged
	// PresetGone keeps branches whose upstream ref no longer exists.
	PresetGone
	// PresetLocalOnly keeps branches that have never been pushed.
	PresetLocalOnly
	// PresetWorktree keeps branches checked out in a worktree.
	PresetWorktree
	// PresetDirty keeps branches checked out in a worktree with
	// uncommitted changes.
	PresetDirty
	// PresetStale keeps branches whose last commit is older than 30 days.
	PresetStale
)

// staleAge is the commit age after which a branch is considered stale.
const staleAge = 30 * 24 * time.Hour

// Presets lists every preset, in display order.
var Presets = []Preset{
	PresetAll,
	PresetMerged,
	PresetGone,
	PresetLocalOnly,
	PresetWorktree,
	PresetDirty,
	PresetStale,
}

// Filter returns the branches in bs matching preset p and, when text is
// non-empty, a case-insensitive substring match on Name.
func Filter(bs []Branch, p Preset, text string, now time.Time) []Branch {
	var out []Branch
	needle := strings.ToLower(text)
	for _, b := range bs {
		if !matchesPreset(b, p, now) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(b.Name), needle) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Label returns the preset's lowercase display name.
func (p Preset) Label() string {
	switch p {
	case PresetMerged:
		return "merged"
	case PresetGone:
		return "gone"
	case PresetLocalOnly:
		return "local-only"
	case PresetWorktree:
		return "worktree"
	case PresetDirty:
		return "dirty"
	case PresetStale:
		return "stale"
	default:
		return "all"
	}
}

// matchesPreset reports whether branch b belongs to preset p.
func matchesPreset(b Branch, p Preset, now time.Time) bool {
	switch p {
	case PresetMerged:
		return b.Lookup == PRFound && b.PR != nil && b.PR.State == "MERGED"
	case PresetGone:
		return b.Upstream != nil && b.Upstream.Gone
	case PresetLocalOnly:
		return b.Upstream == nil
	case PresetWorktree:
		return b.Worktree != nil
	case PresetDirty:
		return b.Worktree != nil && b.Worktree.Dirty
	case PresetStale:
		return now.Sub(b.CommitAt) > staleAge
	default:
		return true
	}
}
