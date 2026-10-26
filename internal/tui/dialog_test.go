package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/localshred/kk-branches/internal/app"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/koolkit/pkg/ui"
)

// dialog returns the current delete dialog's text with ANSI sequences
// stripped.
func dialog(s screen) string {
	if len(s.confirm) == 0 {
		return ""
	}
	return ui.StripANSI(s.confirm[0].view())
}

// submit presses enter in the new-branch dialog, runs the resulting
// Create command, and feeds its result back to s.
func submit(t *testing.T, s screen) (screen, tea.Cmd) {
	t.Helper()
	s, cmd := press(t, s, "enter")
	if cmd == nil {
		t.Fatalf("enter returned no command")
	}
	m, cmd := s.Update(cmd())
	return m.(screen), cmd
}

func TestDeleteCollectsErrors(t *testing.T) {
	f := &fakeActions{
		snap:       snapshot(safeBranch("alice/a"), safeBranch("alice/b")),
		deleteErrs: map[string]error{"alice/b": errors.New("locked")},
	}
	s := loaded(t, f)

	s, cmd := press(t, s, "a", "b", "d", "y")
	if cmd == nil {
		t.Fatalf("accepting the dialog returned no delete command")
	}
	s = send(t, s, cmd())

	if want := []string{"alice/a", "alice/b"}; !reflect.DeepEqual(f.deleted, want) {
		t.Errorf("Delete calls = %v, want %v", f.deleted, want)
	}
	if !strings.Contains(view(s), "delete failed: alice/b: locked") {
		t.Errorf("View() missing per-branch delete error:\n%s", view(s))
	}
}

func TestDeleteHighlightedWhenNothingSelected(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/merged"), confirmBranch("alice/local"))}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "b", "d")

	if len(s.confirm) != 1 {
		t.Fatalf("got %d dialogs, want only the safe one", len(s.confirm))
	}
	if !strings.Contains(view(s), "Delete 1 merged branches?") {
		t.Errorf("View() missing safe dialog for the highlighted row:\n%s", view(s))
	}
	if d := dialog(s); strings.Contains(d, "alice/local") {
		t.Errorf("dialog includes alice/local, which is neither selected nor highlighted:\n%s", d)
	}
}

func TestDeleteMixedTiersTwoDialogs(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/merged"), confirmBranch("alice/local"))}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "space", "down", "space", "b", "d")
	out := dialog(s)
	if !strings.Contains(out, "Delete 1 merged branches?") || !strings.Contains(out, "alice/merged") {
		t.Fatalf("first dialog does not list the safe branch:\n%s", out)
	}
	if strings.Contains(out, "alice/local") {
		t.Errorf("first dialog lists the confirm branch:\n%s", out)
	}
	if !strings.Contains(view(s), out) {
		t.Errorf("View() does not show the first dialog:\n%s", view(s))
	}

	s, _ = press(t, s, "y")
	out = dialog(s)
	if !strings.Contains(out, "These branches may hold unmerged work:") {
		t.Fatalf("second dialog title missing:\n%s", out)
	}
	if !strings.Contains(out, "alice/local — never pushed") {
		t.Errorf("second dialog missing reasons:\n%s", out)
	}

	s, cmd := press(t, s, "n")
	if cmd == nil {
		t.Fatalf("declining the second dialog returned no delete command")
	}
	s = send(t, s, cmd())

	if want := []string{"alice/merged"}; !reflect.DeepEqual(f.deleted, want) {
		t.Errorf("Delete calls = %v, want %v", f.deleted, want)
	}
	if s.confirm != nil {
		t.Errorf("confirm dialog still open after both answered")
	}
}

func TestDeleteDialogsListWorktreeRemoval(t *testing.T) {
	merged := safeBranch("alice/merged")
	merged.Worktree = &model.Worktree{Path: "/r/app-wt-alice-merged", Branch: "alice/merged"}
	local := confirmBranch("alice/local")
	local.Worktree = &model.Worktree{Path: "/r/app-wt-alice-local", Branch: "alice/local"}
	f := &fakeActions{snap: snapshot(merged, local)}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "space", "down", "space", "b", "d")
	if out := dialog(s); !strings.Contains(out, "alice/merged — removes worktree app-wt-alice-merged") {
		t.Errorf("safe dialog missing worktree removal:\n%s", out)
	}

	s, _ = press(t, s, "y")
	want := "alice/local — never pushed; no PR found; removes worktree app-wt-alice-local"
	if out := dialog(s); !strings.Contains(out, want) {
		t.Errorf("confirm dialog missing %q:\n%s", want, out)
	}
}

func TestNewBranchCheckoutQuits(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x")), bases: []string{"origin/main", "origin/release-2"}}
	s := loaded(t, f)

	s, _ = press(t, s, "b", "n", "f", "i", "x")
	s, cmd := submit(t, s)

	want := app.CreateRequest{Name: "alice/fix", Base: "origin/main", Then: app.ThenCheckout}
	if len(f.creates) != 1 || f.creates[0] != want {
		t.Fatalf("Create calls = %+v, want [%+v]", f.creates, want)
	}
	if cmd == nil {
		t.Fatalf("successful checkout returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("successful checkout did not quit")
	}
	if s.exit.Message != "switched to alice/fix" {
		t.Errorf("Exit.Message = %q, want %q", s.exit.Message, "switched to alice/fix")
	}
}

func TestNewBranchCreateOnlyReloads(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x")), bases: []string{"origin/main"}}
	s := loaded(t, f)

	s, _ = press(t, s, "b", "n", "y", "right", "right")
	s, cmd := submit(t, s)

	if got := f.creates[0].Then; got != app.ThenCreate {
		t.Errorf("Then = %v, want ThenCreate", got)
	}
	if s.newBranch != nil {
		t.Errorf("dialog still open after just-create")
	}
	if cmd == nil {
		t.Fatalf("just-create returned no reload command")
	}
	if _, ok := cmd().(loadedMsg); !ok {
		t.Errorf("just-create command does not reload")
	}
	if !strings.Contains(view(s), "created alice/y") {
		t.Errorf("View() missing created status:\n%s", view(s))
	}
}

func TestNewBranchErrorStaysInDialog(t *testing.T) {
	f := &fakeActions{
		snap:      snapshot(safeBranch("alice/x")),
		bases:     []string{"origin/main"},
		createErr: errors.New("branch alice/x already exists"),
	}
	s := loaded(t, f)

	s, _ = press(t, s, "b", "n", "x")
	s, cmd := submit(t, s)

	if s.newBranch == nil {
		t.Fatalf("dialog closed after Create error")
	}
	if !strings.Contains(view(s), "branch alice/x already exists") {
		t.Errorf("View() missing dialog error:\n%s", view(s))
	}
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Errorf("Create error quit the program")
		}
	}
}

func TestNewBranchFromCyclesToCustomRef(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x")), bases: []string{"origin/main", "origin/release-2"}}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "b", "n", "z", "tab")
	if !strings.Contains(view(s), "origin/release-2") {
		t.Errorf("View() missing second base:\n%s", view(s))
	}
	s, _ = press(t, s, "tab", "tab", "v", "1")
	s, _ = submit(t, s)

	want := app.CreateRequest{Name: "alice/z", Base: "v1", Then: app.ThenCheckout}
	if len(f.creates) != 1 || f.creates[0] != want {
		t.Errorf("Create calls = %+v, want [%+v]", f.creates, want)
	}
}

func TestNewBranchFromIncludesHighlighted(t *testing.T) {
	f := &fakeActions{snap: snapshot(safeBranch("alice/x")), bases: []string{"origin/main"}}
	s := loaded(t, f)

	s, _ = press(t, s, "down", "b", "n", "z", "tab")
	s, _ = submit(t, s)

	if got := f.creates[0].Base; got != "alice/x" {
		t.Errorf("Base = %q, want the highlighted branch alice/x", got)
	}
}

func TestNewBranchWorktreeSetsCdPath(t *testing.T) {
	f := &fakeActions{
		snap:       snapshot(safeBranch("alice/x")),
		bases:      []string{"origin/main"},
		createPath: "/r/app-wt-alice-wt",
	}
	s := loaded(t, f)

	s, _ = press(t, s, "b", "n", "w", "t", "right")
	if !strings.Contains(view(s), "/r/app-wt-alice-wt") {
		t.Errorf("View() missing computed worktree path:\n%s", view(s))
	}
	s, cmd := submit(t, s)

	if got := f.creates[0].Then; got != app.ThenWorktree {
		t.Errorf("Then = %v, want ThenWorktree", got)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("successful worktree create did not quit")
	}
	want := Exit{CdPath: "/r/app-wt-alice-wt", Message: "created worktree /r/app-wt-alice-wt"}
	if s.exit != want {
		t.Errorf("Exit = %+v, want %+v", s.exit, want)
	}
}
