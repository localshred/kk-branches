package model

import (
	"sort"
	"strings"
)

// Sort orders bs in place with trunk branches first (alphabetically by
// name), followed by the rest ordered by commit time descending, then
// by name.
func Sort(bs []Branch) {
	sort.SliceStable(bs, func(i, j int) bool {
		a, b := bs[i], bs[j]
		if a.Trunk != b.Trunk {
			return a.Trunk
		}
		if a.Trunk {
			return a.Name < b.Name
		}
		if !a.CommitAt.Equal(b.CommitAt) {
			return a.CommitAt.After(b.CommitAt)
		}
		return a.Name < b.Name
	})
}

// WorktreePath returns the conventional worktree directory for branch,
// sitting alongside mainCheckout with slashes replaced by dashes.
func WorktreePath(mainCheckout, branch string) string {
	return mainCheckout + "-wt-" + strings.ReplaceAll(branch, "/", "-")
}
