package model

import (
	"strings"
	"testing"
	"time"

	"github.com/localshred/koolkit/pkg/ui"
)

func TestTruncateMiddle(t *testing.T) {
	short := "alice/fix"
	if got := TruncateMiddle(short, 30); got != short {
		t.Errorf("TruncateMiddle(short) = %q, want unchanged %q", got, short)
	}

	long := "alice/feature-login-session-refresh-cache"
	got := TruncateMiddle(long, 30)
	if n := len([]rune(got)); n != 30 {
		t.Errorf("TruncateMiddle(long) rune length = %d, want 30: %q", n, got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("TruncateMiddle(long) = %q, want to contain ellipsis", got)
	}

	multibyte := "日本語のブランチ名はとても長くなることがあります"
	got = TruncateMiddle(multibyte, 10)
	if n := len([]rune(got)); n != 10 {
		t.Errorf("TruncateMiddle(multibyte) rune length = %d, want 10: %q", n, got)
	}
}

func TestOriginCell(t *testing.T) {
	tests := []struct {
		name string
		u    *Upstream
		want string
	}{
		{"nil", nil, "local only"},
		{"gone", &Upstream{Gone: true}, "gone"},
		{"synced", &Upstream{Ahead: 0, Behind: 0}, "="},
		{"ahead", &Upstream{Ahead: 2}, "↑2"},
		{"behind", &Upstream{Behind: 1}, "↓1"},
		{"diverged", &Upstream{Ahead: 2, Behind: 1}, "↑2↓1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ui.StripANSI(OriginCell(tt.u))
			if got != tt.want {
				t.Errorf("OriginCell(%+v) = %q, want %q", tt.u, got, tt.want)
			}
		})
	}
}

func TestPRCellHyperlink(t *testing.T) {
	b := Branch{
		Lookup: PRFound,
		PR:     &PR{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN"},
	}

	got := PRCell(b)

	want := "\x1b]8;;https://github.com/o/r/pull/7\x1b\\"
	if !strings.Contains(got, want) {
		t.Errorf("PRCell = %q, want to contain %q", got, want)
	}
}

func TestPRCellStates(t *testing.T) {
	tests := []struct {
		name string
		b    Branch
		want string
	}{
		{"pending", Branch{Lookup: PRPending}, "…"},
		{"unknown", Branch{Lookup: PRUnknown}, "?"},
		{"none", Branch{Lookup: PRNone}, "—"},
		{"found without a PR", Branch{Lookup: PRFound}, "—"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ui.StripANSI(PRCell(tt.b))
			if got != tt.want {
				t.Errorf("PRCell = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBranchLabelShowsUpstreamHead(t *testing.T) {
	b := Branch{
		Name:     "alice/fix",
		Upstream: &Upstream{Head: "alice/fix-renamed"},
	}

	got := ui.StripANSI(BranchLabel(b, 30))

	want := "alice/fix → alice/fix-renamed"
	if got != want {
		t.Errorf("BranchLabel = %q, want %q", got, want)
	}
}

func TestBranchLabelNoArrowWhenSameHead(t *testing.T) {
	b := Branch{
		Name:     "alice/fix",
		Upstream: &Upstream{Head: "alice/fix"},
	}

	got := ui.StripANSI(BranchLabel(b, 30))

	if got != "alice/fix" {
		t.Errorf("BranchLabel = %q, want %q", got, "alice/fix")
	}
}

func TestAgeCell(t *testing.T) {
	now := time.Now()
	b := Branch{CommitAt: now.Add(-2 * time.Hour)}

	got := ui.StripANSI(AgeCell(b, now))

	want := ui.StripANSI(ui.AgeCell(2 * time.Hour))
	if got != want {
		t.Errorf("AgeCell = %q, want %q", got, want)
	}
}
