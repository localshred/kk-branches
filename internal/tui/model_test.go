package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/localshred/kk-branches/internal/app"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/kk-branches/internal/syncer"
	"github.com/localshred/koolkit/pkg/ui"
)

// testNow is the fixed clock every test screen renders against.
var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// fakeActions is a recording Actions whose Load and LoadPRs return
// scripted snapshots.
type fakeActions struct {
	snap       model.Snapshot
	prSnap     *model.Snapshot
	prErr      error
	bases      []string
	checkouts  []string
	createPath string
	createErr  error
	creates    []app.CreateRequest
	deleted    []string
	deleteErrs map[string]error
	opened     []string
	synced     int
	worktrees  []string
}

func (f *fakeActions) AddWorktree(_ model.Snapshot, branch string) (string, error) {
	f.worktrees = append(f.worktrees, branch)
	return "/r/app-wt-" + branch, nil
}

func (f *fakeActions) Bases(model.Snapshot) []string { return f.bases }

func (f *fakeActions) Checkout(_ model.Snapshot, b model.Branch) error {
	f.checkouts = append(f.checkouts, b.Name)
	return nil
}

func (f *fakeActions) Create(_ model.Snapshot, req app.CreateRequest) (string, error) {
	f.creates = append(f.creates, req)
	return f.createPath, f.createErr
}

func (f *fakeActions) Delete(b model.Branch) error {
	f.deleted = append(f.deleted, b.Name)
	return f.deleteErrs[b.Name]
}

func (f *fakeActions) Load() (model.Snapshot, error) { return f.snap, nil }

func (f *fakeActions) LoadPRs(s model.Snapshot) (model.Snapshot, error) {
	if f.prSnap != nil {
		return *f.prSnap, f.prErr
	}
	return s, f.prErr
}

func (f *fakeActions) Open(url string) error {
	f.opened = append(f.opened, url)
	return nil
}

func (f *fakeActions) RemoveWorktree(model.Branch) error { return nil }

func (f *fakeActions) Sync([]model.Branch, func(syncer.Event)) error {
	f.synced++
	return nil
}

// confirmBranch is a never-pushed branch with no PR, so Assess rates
// it Confirm with "never pushed; no PR found".
func confirmBranch(name string) model.Branch {
	return model.Branch{Name: name, CommitAt: testNow.Add(-time.Hour), Lookup: model.PRNone}
}

// key builds the KeyPressMsg Bubble Tea delivers for a keystroke name
// such as "a", "W", "space", "enter", or "shift+tab".
func key(name string) tea.KeyPressMsg {
	special := map[string]tea.KeyPressMsg{
		"backspace": {Code: tea.KeyBackspace},
		"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
		"down":      {Code: tea.KeyDown},
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"left":      {Code: tea.KeyLeft},
		"right":     {Code: tea.KeyRight},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
		"space":     {Code: tea.KeySpace, Text: " "},
		"tab":       {Code: tea.KeyTab},
		"up":        {Code: tea.KeyUp},
	}
	if k, ok := special[name]; ok {
		return k
	}
	r := []rune(name)[0]
	if strings.ToUpper(name) == name && strings.ToLower(name) != name {
		return tea.KeyPressMsg{Code: []rune(strings.ToLower(name))[0], Text: name, Mod: tea.ModShift}
	}
	return tea.KeyPressMsg{Code: r, Text: name}
}

// loaded returns a screen over f that has completed its initial Load
// and LoadPRs round trip.
func loaded(t *testing.T, f *fakeActions) screen {
	t.Helper()
	s := newScreen(f, func() time.Time { return testNow })
	m, cmd := s.Update(s.Init()())
	return send(t, m.(screen), cmd())
}

// press feeds each key to s in order and returns the resulting screen
// and the command returned by the last key.
func press(t *testing.T, s screen, keys ...string) (screen, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var m tea.Model
		m, cmd = s.Update(key(k))
		s = m.(screen)
	}
	return s, cmd
}

// rowFor returns the first line of out containing needle.
func rowFor(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", needle, out)
	return ""
}

// safeBranch is a branch whose PR merged and whose upstream is gone,
// so Assess rates it Safe.
func safeBranch(name string) model.Branch {
	return model.Branch{
		Name:     name,
		CommitAt: testNow.Add(-2 * time.Hour),
		Upstream: &model.Upstream{Ref: "origin/" + name, Head: name, Gone: true},
		Lookup:   model.PRFound,
		PR:       &model.PR{Number: 7, URL: "https://github.com/o/r/pull/7", State: "MERGED"},
		InPR:     true,
	}
}

// send feeds msg to s and returns the resulting screen.
func send(t *testing.T, s screen, msg tea.Msg) screen {
	t.Helper()
	m, _ := s.Update(msg)
	return m.(screen)
}

// snapshot wraps branches in a snapshot with a trunk and prefix.
func snapshot(bs ...model.Branch) model.Snapshot {
	trunk := model.Branch{Name: "main", Trunk: true, CommitAt: testNow.Add(-time.Minute)}
	return model.Snapshot{
		Branches: append([]model.Branch{trunk}, bs...),
		MainPath: "/r/app",
		Prefix:   "alice/",
		Repo:     "app",
		Trunk:    "main",
	}
}

// view returns s's rendered content with ANSI sequences stripped.
func view(s screen) string {
	return ui.StripANSI(s.View().Content)
}

func TestBlockedRowNotSelectable(t *testing.T) {
	blocked := model.Branch{
		Name:     "alice/dirty",
		CommitAt: testNow.Add(-time.Hour),
		Lookup:   model.PRNone,
		Worktree: &model.Worktree{Path: "/r/app-wt-alice-dirty", Branch: "alice/dirty", Dirty: true},
	}
	f := &fakeActions{snap: snapshot(blocked)}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "space")

	if n := len(s.selected); n != 0 {
		t.Errorf("selected = %v, want none", s.selected)
	}
	if !strings.Contains(view(s), "blocked") {
		t.Errorf("View() missing %q:\n%s", "blocked", view(s))
	}
	if !strings.Contains(view(s), "0 selected") {
		t.Errorf("View() missing %q:\n%s", "0 selected", view(s))
	}
}

func TestBranchMenuChecksOutHighlighted(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, cmd := press(t, s, "down", "b", "c")
	if cmd == nil {
		t.Fatalf("b c returned no command")
	}
	s = send(t, s, cmd())

	if want := []string{"alice/x"}; !reflect.DeepEqual(f.checkouts, want) {
		t.Errorf("Checkout calls = %v, want %v", f.checkouts, want)
	}
	if out := view(s); !strings.Contains(out, "checked out alice/x in /r/app") {
		t.Errorf("View() missing checkout status:\n%s", out)
	}
}

func TestClearSelection(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/a"), safeBranch("alice/b"))}
	s := loaded(t, f)

	s, _ = press(t, s, "a", "A")

	if n := len(s.selected); n != 0 {
		t.Errorf("selected = %v, want none", s.selected)
	}
}

func TestCopyBranchName(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, cmd := press(t, s, "down", "y")
	if cmd == nil {
		t.Fatalf("y returned no command")
	}

	if got, want := cmd(), tea.SetClipboard("alice/x")(); got != want {
		t.Errorf("y message = %#v, want %#v", got, want)
	}
	if out := view(s); !strings.Contains(out, "copied alice/x") {
		t.Errorf("View() missing copy status:\n%s", out)
	}
}

func TestCursorRowHighlighted(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)
	s = send(t, s, tea.WindowSizeMsg{Width: 120, Height: 40})

	s, _ = press(t, s, "down")
	out := s.View().Content

	row := rowFor(t, out, "alice/x")
	if !strings.HasPrefix(row, cursorBackground) {
		t.Errorf("cursor row does not start with the highlight: %q", row)
	}
	if !strings.HasSuffix(row, ansiReset) {
		t.Errorf("cursor row does not end with a reset: %q", row)
	}
	resets := strings.Count(row, ansiReset)
	if reapplied := strings.Count(row, ansiReset+cursorBackground); reapplied != resets-1 {
		t.Errorf("highlight re-applied after %d of %d inner resets: %q", reapplied, resets-1, row)
	}
	if n := ui.VisibleLen(row); n != 120 {
		t.Errorf("cursor row visible width = %d, want 120", n)
	}
	if trunk := rowFor(t, out, "★"); strings.Contains(trunk, cursorBackground) {
		t.Errorf("non-cursor row is highlighted: %q", trunk)
	}
}

func TestInfoPanelFollowsCursor(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/a"), confirmBranch("alice/b"))}
	s := loaded(t, f)
	want := map[string]string{
		"alice/a": "safe deletion: safe",
		"alice/b": "safe deletion: confirm",
	}

	s, _ = press(t, s, "down", "i")
	for range 2 {
		b, _ := s.highlighted()
		out := view(s)
		for name, text := range want {
			if got := strings.Contains(out, text); got != (name == b.Name) {
				t.Errorf("cursor on %s: View() contains %q = %v:\n%s", b.Name, text, got, out)
			}
		}
		s, _ = press(t, s, "down")
	}
}

func TestInfoPanelShowsRiskReasons(t *testing.T) {
	f := &fakeActions{snap: snapshot(confirmBranch("alice/new"))}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "i")
	out := view(s)
	for _, text := range []string{"safe deletion: confirm", "· never pushed", "· no PR found"} {
		if !strings.Contains(out, text) {
			t.Errorf("View() missing %q:\n%s", text, out)
		}
	}

	s, _ = press(t, s, "esc")
	if out := view(s); strings.Contains(out, "never pushed") {
		t.Errorf("esc left the info panel open:\n%s", out)
	}
}

func TestInfoPanelShowsSafeAndTrunkReasons(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, _ = press(t, s, "i")
	if out := view(s); !strings.Contains(out, "trunks are never deleted") {
		t.Errorf("trunk info missing reason:\n%s", out)
	}

	s, _ = press(t, s, "down")
	if out := view(s); !strings.Contains(out, "PR #7 merged and contains the branch tip") {
		t.Errorf("safe info missing reason:\n%s", out)
	}
}

func TestMenuShowsKeysAndEscCancels(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "b")
	out := view(s)
	for _, text := range []string{"c  checkout in main repo", "d  delete", "n  new branch", "esc cancel"} {
		if !strings.Contains(out, text) {
			t.Errorf("branch menu missing %q:\n%s", text, out)
		}
	}

	s, _ = press(t, s, "esc", "d")
	if out := view(s); strings.Contains(out, "checkout in main repo") {
		t.Errorf("esc left the menu open:\n%s", out)
	}
	if s.confirm != nil {
		t.Errorf("d without the b prefix opened a delete dialog")
	}
}

func TestOpenPROpensHighlightedURL(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, cmd := press(t, s, "down", "o")
	if cmd == nil {
		t.Fatalf("o returned no command")
	}
	s = send(t, s, cmd())

	want := "https://github.com/o/r/pull/7"
	if len(f.opened) != 1 || f.opened[0] != want {
		t.Errorf("Open calls = %v, want [%s]", f.opened, want)
	}
	if !strings.Contains(view(s), "opened PR #7") {
		t.Errorf("View() missing opened status:\n%s", view(s))
	}
}

func TestPRLoadErrorShowsUnknown(t *testing.T) {
	unknown := model.Branch{Name: "alice/x", CommitAt: testNow.Add(-time.Hour), Lookup: model.PRUnknown}
	prSnap := snapshot(unknown)
	f := &fakeActions{
		snap:   snapshot(model.Branch{Name: "alice/x", CommitAt: testNow.Add(-time.Hour), Lookup: model.PRPending}),
		prSnap: &prSnap,
		prErr:  errors.New("gh: not logged in"),
	}
	s := loaded(t, f)

	out := view(s)
	if row := rowFor(t, out, "alice/x"); !strings.Contains(row, "?") {
		t.Errorf("row = %q, want PR cell %q", row, "?")
	}
	if !strings.Contains(out, "PR lookup failed: gh: not logged in") {
		t.Errorf("View() missing PR lookup failure status:\n%s", out)
	}

	s, _ = press(t, s, "a")
	if n := len(s.selected); n != 0 {
		t.Errorf("after a, selected = %v, want none", s.selected)
	}
}

func TestPendingRowsShowEllipsisUntilPRsLoad(t *testing.T) {
	pending := model.Branch{Name: "alice/x", CommitAt: testNow.Add(-time.Hour), Lookup: model.PRPending}
	f := &fakeActions{snap: snapshot(pending)}
	s := newScreen(f, func() time.Time { return testNow })
	s = send(t, s, s.Init()())

	s, _ = press(t, s, "down", "space")
	if n := len(s.selected); n != 0 {
		t.Errorf("selected pending row: %v, want none", s.selected)
	}
	if row := rowFor(t, view(s), "alice/x"); !strings.Contains(row, "…") {
		t.Errorf("pending row = %q, want it to contain …", row)
	}
}

func TestPresetTabFiltersRows(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/merged"), confirmBranch("alice/local"))}
	s := loaded(t, f)

	s, _ = press(t, s, "tab")

	out := view(s)
	if !strings.Contains(out, "[merged 1]") {
		t.Errorf("View() missing active merged preset:\n%s", out)
	}
	if strings.Contains(out, "alice/local") {
		t.Errorf("merged preset shows alice/local:\n%s", out)
	}
}

func TestRiskColumnHeader(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	if out := view(s); !strings.Contains(out, "SAFE DELETION?") {
		t.Errorf("View() missing risk header:\n%s", out)
	}

	s, _ = press(t, s, "s")
	s = send(t, s, syncer.Event{Branch: "alice/x", Status: syncer.StatusUpToDate, Done: 1, Total: 1})
	out := view(s)
	if !strings.Contains(out, "SYNC RESULT") || strings.Contains(out, "SAFE DELETION?") {
		t.Errorf("View() after sync events should label the column SYNC RESULT:\n%s", out)
	}
}

func TestSelectAllSafeRespectsFilter(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/login"), safeBranch("alice/logout"))}
	s := loaded(t, f)

	s, _ = press(t, s, "/", "o", "u", "t", "enter", "a")

	if len(s.selected) != 1 || !s.selected["alice/logout"] {
		t.Errorf("selected = %v, want only alice/logout", s.selected)
	}
}

func TestSyncDisablesMutatingKeys(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)
	s, _ = press(t, s, "s")

	s, _ = press(t, s, "down", "b", "d")

	if len(f.deleted) != 0 {
		t.Errorf("Delete called with %v during sync, want no calls", f.deleted)
	}
	if s.confirm != nil {
		t.Errorf("confirm dialog opened during sync")
	}
	if !strings.Contains(view(s), "sync running") {
		t.Errorf("View() missing %q:\n%s", "sync running", view(s))
	}
}

func TestSyncEventsUpdateRows(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, _ = press(t, s, "s")
	s = send(t, s, syncer.Event{Branch: "alice/x", Status: syncer.StatusRebased, Detail: "+3", Done: 1, Total: 2})

	out := view(s)
	if row := rowFor(t, out, "alice/x"); !strings.Contains(row, "rebased +3") {
		t.Errorf("row = %q, want it to contain %q", row, "rebased +3")
	}
	if !strings.Contains(out, "syncing 1/2") {
		t.Errorf("View() missing %q:\n%s", "syncing 1/2", out)
	}
}

func TestSyncFailureRouting(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, _ = press(t, s, "s")
	s = send(t, s, syncer.Event{Branch: "alice/x", Status: syncer.StatusFailed, Detail: "bad ref"})
	s = send(t, s, syncer.Event{Status: syncer.StatusFailed, Detail: "fetch refused"})

	out := view(s)
	if row := rowFor(t, out, "alice/x"); !strings.Contains(row, "failed: bad ref") {
		t.Errorf("row = %q, want per-branch failure in its slot", row)
	}
	if !strings.Contains(out, "sync failed: fetch refused") {
		t.Errorf("View() missing whole-run failure status:\n%s", out)
	}
}

func TestSyncFinishedReloads(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)
	s, _ = press(t, s, "s")
	s = send(t, s, syncer.Event{Branch: "alice/x", Status: syncer.StatusUpToDate})

	m, cmd := s.Update(syncFinishedMsg{})
	s = m.(screen)

	if s.syncing {
		t.Errorf("syncing = true after syncFinishedMsg")
	}
	if cmd == nil {
		t.Fatalf("syncFinishedMsg returned no reload command")
	}
	if _, ok := cmd().(loadedMsg); !ok {
		t.Errorf("syncFinishedMsg command does not reload")
	}
	if row := rowFor(t, view(s), "alice/x"); !strings.Contains(row, "up to date") {
		t.Errorf("row = %q, want sync result kept until next refresh", row)
	}
}

func TestSyncSecondCtrlCForceQuits(t *testing.T) {
	for _, first := range []string{"ctrl+c", "q"} {
		t.Run(first, func(t *testing.T) {
			f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
			s := loaded(t, f)
			s, _ = press(t, s, "s")

			s, cmd := press(t, s, first)
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); ok {
					t.Fatalf("first %s quit while sync running", first)
				}
			}
			if !strings.Contains(view(s), "press ctrl+c again to force quit") {
				t.Errorf("View() missing force-quit hint:\n%s", view(s))
			}

			s, cmd = press(t, s, "ctrl+c")
			if cmd == nil {
				t.Fatalf("second ctrl+c returned no command")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("second ctrl+c did not quit")
			}
			if !strings.Contains(s.exit.Message, "sync interrupted") || !strings.Contains(s.exit.Message, "git stash list") {
				t.Errorf("Exit.Message = %q, want interrupted-sync warning", s.exit.Message)
			}
		})
	}
}

func TestSyncQuitWaitsForSync(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)
	s, _ = press(t, s, "s")

	s, cmd := press(t, s, "q")
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatalf("q quit while sync running")
		}
	}

	_, cmd = s.Update(syncFinishedMsg{})
	if cmd == nil {
		t.Fatalf("syncFinishedMsg after q returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("syncFinishedMsg after q did not quit")
	}
}

func TestTruncatesLongNames(t *testing.T) {
	long := "alice/" + strings.Repeat("abcdefghij", 5) + "wxyz"
	if n := len([]rune(long)); n != 60 {
		t.Fatalf("test name is %d runes, want 60", n)
	}
	f := &fakeActions{snap: snapshot(safeBranch(long))}
	s := loaded(t, f)

	out := view(s)
	if strings.Contains(out, long) {
		t.Errorf("View() contains full 60-rune name:\n%s", out)
	}
	if row := rowFor(t, out, "alice/abc"); !strings.Contains(row, "…") {
		t.Errorf("row = %q, want truncated name with …", row)
	}
}

func TestWorktreeColumn(t *testing.T) {
	wt := safeBranch("alice/wt")
	wt.Worktree = &model.Worktree{Path: "/r/app-wt-alice-wt", Branch: "alice/wt", Dirty: true}
	f := &fakeActions{snap: snapshot(wt)}
	f.snap.Branches[0].Worktree = &model.Worktree{Path: "/r/app", Branch: "main", Main: true}
	s := loaded(t, f)

	out := view(s)
	if row := rowFor(t, out, "alice/wt"); !strings.Contains(row, "app-wt-alice-wt ✎ dirty") {
		t.Errorf("row = %q, want relative worktree path and dirty marker", row)
	}
	if row := rowFor(t, out, "★"); !strings.Contains(row, "(main checkout)") {
		t.Errorf("trunk row = %q, want (main checkout)", row)
	}
}

func TestWorktreeGoQuitsWithCdPath(t *testing.T) {
	b := safeBranch("alice/x")
	b.Worktree = &model.Worktree{Path: "/r/app-wt-alice-x", Branch: "alice/x"}
	f := &fakeActions{snap: snapshot(b, safeBranch("alice/y"))}
	s := loaded(t, f)

	s, cmd := press(t, s, "down", "w", "g")
	if cmd == nil {
		t.Fatalf("w g returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("w g did not quit")
	}
	if want := (Exit{CdPath: "/r/app-wt-alice-x"}); s.exit != want {
		t.Errorf("Exit = %+v, want %+v", s.exit, want)
	}
}

func TestWorktreeGoWithoutWorktree(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	s, cmd := press(t, s, "down", "w", "g")

	if cmd != nil {
		t.Errorf("w g on a row without a worktree returned a command")
	}
	if out := view(s); !strings.Contains(out, "alice/x has no worktree") {
		t.Errorf("View() missing no-worktree status:\n%s", out)
	}
}

func TestWorktreeMenuAddsWorktree(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x"))}
	s := loaded(t, f)

	_, cmd := press(t, s, "down", "w", "a")
	if cmd == nil {
		t.Fatalf("w a returned no command")
	}
	cmd()

	if want := []string{"alice/x"}; !reflect.DeepEqual(f.worktrees, want) {
		t.Errorf("AddWorktree calls = %v, want %v", f.worktrees, want)
	}
}
