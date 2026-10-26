package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/localshred/koolkit/pkg/ui"
)

// ansiPurple colors a merged PR's state text; koolkit's ui package has no
// purple helper.
const ansiPurple = "\x1b[35m"

// ansiReset ends an ANSI color sequence started by ansiPurple.
const ansiReset = "\x1b[0m"

// AgeCell renders b's commit age as a colored, humanized duration.
func AgeCell(b Branch, now time.Time) string {
	return ui.AgeCell(now.Sub(b.CommitAt))
}

// BranchLabel renders b's name truncated to max runes, with a dim arrow
// to its upstream head when that differs from the branch name.
func BranchLabel(b Branch, max int) string {
	label := TruncateMiddle(b.Name, max)
	if b.Upstream != nil && b.Upstream.Head != b.Name {
		label += ui.Dim(" → " + b.Upstream.Head)
	}
	return label
}

// OriginCell renders a branch's relationship to its upstream ref: "local
// only" when there is none, "gone" when the remote ref was deleted, "="
// when fully in sync, or arrows showing how far ahead and/or behind the
// branch is.
func OriginCell(u *Upstream) string {
	switch {
	case u == nil:
		return ui.Yellow("local only")
	case u.Gone:
		return ui.Dim("gone")
	case u.Ahead == 0 && u.Behind == 0:
		return ui.Green("=")
	default:
		var sb strings.Builder
		if u.Ahead > 0 {
			sb.WriteString("↑" + strconv.Itoa(u.Ahead))
		}
		if u.Behind > 0 {
			sb.WriteString("↓" + strconv.Itoa(u.Behind))
		}
		return ui.Yellow(sb.String())
	}
}

// PRCell renders a branch's pull-request lookup state: a placeholder
// while pending or unknown, a dim dash when there isn't one, or a
// hyperlinked PR number with its state and, for open PRs, a checks
// glyph.
func PRCell(b Branch) string {
	switch {
	case b.Lookup == PRPending:
		return "…"
	case b.Lookup == PRUnknown:
		return "?"
	case b.Lookup == PRFound && b.PR != nil:
		return ui.Hyperlink(b.PR.URL, ui.Cyan("#"+strconv.Itoa(b.PR.Number))) + " " + prStateCell(*b.PR)
	default:
		return ui.Dim("—")
	}
}

// TruncateMiddle shortens s to at most max runes by replacing its center
// with an ellipsis, leaving s unchanged when it already fits.
func TruncateMiddle(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	lead := max / 2
	trail := (max - 1) / 2
	return string(runes[:lead]) + "…" + string(runes[len(runes)-trail:])
}

// checksGlyph returns the glyph for an open PR's check state.
func checksGlyph(checks string) string {
	switch checks {
	case "passing":
		return " ✅"
	case "failing":
		return " ❌"
	case "running":
		return " 🔄"
	default:
		return ""
	}
}

// prStateCell renders a found PR's state, colored by outcome.
func prStateCell(pr PR) string {
	state := strings.ToLower(pr.State)
	switch pr.State {
	case "MERGED":
		return ansiPurple + state + ansiReset
	case "CLOSED":
		return ui.Red(state)
	case "OPEN":
		return ui.Green(state) + checksGlyph(pr.Checks)
	default:
		return state
	}
}
