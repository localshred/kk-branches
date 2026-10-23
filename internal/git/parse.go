package git

import (
	"strconv"
	"strings"
	"time"

	"github.com/localshred/kk-branches/internal/model"
)

// ParseBranches parses the NUL-separated, newline-delimited output of the
// for-each-ref format used by Repo.Branches into model.Branch values.
func ParseBranches(out string) []model.Branch {
	branches := []model.Branch{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		for len(fields) < 7 {
			fields = append(fields, "")
		}
		commitSeconds, _ := strconv.ParseInt(fields[3], 10, 64)
		branch := model.Branch{
			Name:        fields[0],
			Tip:         fields[1],
			AuthorEmail: strings.Trim(fields[2], "<>"),
			CommitAt:    time.Unix(commitSeconds, 0),
			Upstream:    parseUpstream(fields[4], fields[5], fields[6]),
		}
		branches = append(branches, branch)
	}
	return branches
}

// ParseWorktrees parses the porcelain output of `git worktree list` into
// model.Worktree values. The first entry is marked Main; the entry whose
// Path matches currentDir is marked Current.
func ParseWorktrees(out string, currentDir string) []model.Worktree {
	worktrees := []model.Worktree{}
	var cur *model.Worktree
	flush := func() {
		if cur != nil {
			worktrees = append(worktrees, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &model.Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			if cur != nil {
				cur.Head = strings.TrimPrefix(line, "HEAD ")
			}
		case strings.HasPrefix(line, "branch "):
			if cur != nil {
				ref := strings.TrimPrefix(line, "branch ")
				cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
			}
		}
	}
	flush()

	normalized := strings.TrimSuffix(currentDir, "/")
	for i := range worktrees {
		worktrees[i].Main = i == 0
		worktrees[i].Current = strings.TrimSuffix(worktrees[i].Path, "/") == normalized
	}
	return worktrees
}

// parseUpstream builds an Upstream from the raw upstream:short,
// upstream:remoteref, and upstream:track field values. It returns nil when
// short is empty, meaning the branch has no upstream configured.
func parseUpstream(short, remoteRef, track string) *model.Upstream {
	if short == "" {
		return nil
	}
	ahead, behind, gone := parseTrack(track)
	return &model.Upstream{
		Ref:    short,
		Head:   strings.TrimPrefix(remoteRef, "refs/heads/"),
		Gone:   gone,
		Ahead:  ahead,
		Behind: behind,
	}
}

// parseTrack parses the nobracket upstream:track value, e.g. "ahead 2,
// behind 1" or "gone".
func parseTrack(track string) (ahead int, behind int, gone bool) {
	if track == "" {
		return 0, 0, false
	}
	if track == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(track, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind, false
}
