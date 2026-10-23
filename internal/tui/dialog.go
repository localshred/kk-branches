package tui

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/localshred/kk-branches/internal/app"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/koolkit/pkg/ui"
)

// customChoice is the last "from" option, which opens a ref input.
const customChoice = "custom…"

// thenLabels names each app.Then option, indexed by its value.
var thenLabels = []string{
	app.ThenCheckout: "check out here",
	app.ThenWorktree: "new worktree",
	app.ThenCreate:   "just create",
}

// confirmDialog asks a yes/no question about deleting branches.
type confirmDialog struct {
	branches []model.Branch
	lines    []string
	title    string
}

// createCmd runs Actions.Create for req.
func createCmd(a Actions, snap model.Snapshot, req app.CreateRequest) tea.Cmd {
	return func() tea.Msg {
		path, err := a.Create(snap, req)
		return createdMsg{err: err, path: path, req: req}
	}
}

// createdMsg carries the result of Actions.Create.
type createdMsg struct {
	err  error
	path string
	req  app.CreateRequest
}

// cycleBase moves "from" to the next choice, moving focus to the ref
// input when it lands on customChoice and back to the name otherwise.
func (d newBranchDialog) cycleBase() (newBranchDialog, tea.Cmd) {
	d.choice = (d.choice + 1) % len(d.choices)
	if d.choices[d.choice] == customChoice {
		d.name.Blur()
		return d, d.custom.Focus()
	}
	d.custom.Blur()
	if d.name.Focused() {
		return d, nil
	}
	return d, d.name.Focus()
}

// cycleThen moves the "then" radio by step, wrapping around.
func (d newBranchDialog) cycleThen(step int) newBranchDialog {
	n := len(thenLabels)
	d.then = app.Then((int(d.then) + step + n) % n)
	return d
}

// deleteCmd deletes each branch in bs, collecting per-branch failures.
func deleteCmd(a Actions, bs []model.Branch) tea.Cmd {
	return func() tea.Msg {
		var msg deletedMsg
		for _, b := range bs {
			if err := a.Delete(b); err != nil {
				msg.failures = append(msg.failures, b.Name+": "+err.Error())
				continue
			}
			msg.deleted = append(msg.deleted, b.Name)
		}
		return msg
	}
}

// deleteDialogs builds the confirmation dialogs for deleting targets:
// one for Safe branches, then one for Confirm branches listing why each
// may hold unmerged work. Other levels are left out.
func deleteDialogs(targets []model.Branch) []confirmDialog {
	var safe, confirm []model.Branch
	var safeLines, confirmLines []string
	for _, b := range targets {
		risk := model.Assess(b)
		switch risk.Level {
		case model.LevelSafe:
			safe = append(safe, b)
			safeLines = append(safeLines, b.Name)
		case model.LevelConfirm:
			confirm = append(confirm, b)
			confirmLines = append(confirmLines, b.Name+" — "+strings.Join(risk.Reasons, "; "))
		}
	}
	var out []confirmDialog
	if len(safe) > 0 {
		out = append(out, confirmDialog{
			branches: safe,
			lines:    safeLines,
			title:    "Delete " + strconv.Itoa(len(safe)) + " merged branches?",
		})
	}
	if len(confirm) > 0 {
		out = append(out, confirmDialog{
			branches: confirm,
			lines:    confirmLines,
			title:    ui.Yellow("These branches may hold unmerged work:"),
		})
	}
	return out
}

// deleteTargets opens the delete dialogs for the selection, or for the
// highlighted row when nothing is selected.
func (s screen) deleteTargets() (screen, tea.Cmd) {
	var targets []model.Branch
	for _, b := range s.snap.Branches {
		if s.selected[b.Name] {
			targets = append(targets, b)
		}
	}
	if len(targets) == 0 {
		if b, ok := s.highlighted(); ok {
			targets = []model.Branch{b}
		}
	}
	dialogs := deleteDialogs(targets)
	if len(dialogs) == 0 {
		s.status = errStatus(undeletableText(targets))
		return s, nil
	}
	s.accepted = nil
	s.confirm = dialogs
	return s, nil
}

// deletedMsg carries the result of deleting every accepted branch.
type deletedMsg struct {
	deleted  []string
	failures []string
}

// newBranchDialog collects the name, base, and follow-up for a new
// branch.
type newBranchDialog struct {
	choice     int
	choices    []string
	custom     textinput.Model
	err        string
	mainPath   string
	name       textinput.Model
	submitting bool
	then       app.Then
}

// newNewBranchDialog opens the new-branch dialog with the name
// prefilled with prefix, offering bases, then highlighted (when set),
// then a custom ref as the "from" choices.
func newNewBranchDialog(bases []string, highlighted, prefix, mainPath string) (newBranchDialog, tea.Cmd) {
	name := textinput.New()
	name.Prompt = ""
	name.SetValue(prefix)
	custom := textinput.New()
	custom.Prompt = ""
	custom.Placeholder = "any ref"
	choices := slices.Clone(bases)
	if highlighted != "" {
		choices = append(choices, highlighted)
	}
	d := newBranchDialog{
		choices:  append(choices, customChoice),
		custom:   custom,
		mainPath: mainPath,
		name:     name,
		then:     app.ThenCheckout,
	}
	return d, d.name.Focus()
}

// nextConfirm moves past the current delete dialog, running the
// accepted deletions once every dialog has been answered.
func (s screen) nextConfirm() (tea.Model, tea.Cmd) {
	s.confirm = s.confirm[1:]
	if len(s.confirm) > 0 {
		return s, nil
	}
	accepted := s.accepted
	s.accepted = nil
	s.confirm = nil
	if len(accepted) == 0 {
		s.status = okStatus("nothing deleted")
		return s, nil
	}
	return s, deleteCmd(s.actions, accepted)
}

// onConfirmKey answers the current delete dialog: y accepts its
// branches, n skips them, esc cancels every remaining dialog.
func (s screen) onConfirmKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "y":
		s.accepted = slices.Concat(s.accepted, s.confirm[0].branches)
		return s.nextConfirm()
	case "n":
		return s.nextConfirm()
	case "esc":
		s.accepted = nil
		s.confirm = nil
	}
	return s, nil
}

// onCreated finishes the new-branch flow: errors stay in the dialog;
// checkout and worktree quit with a follow-up; just-create reloads.
func (s screen) onCreated(msg createdMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if s.newBranch == nil {
			s.status = errStatus(msg.err.Error())
			return s, nil
		}
		d := *s.newBranch
		d.err = msg.err.Error()
		d.submitting = false
		s.newBranch = &d
		return s, nil
	}
	switch msg.req.Then {
	case app.ThenCheckout:
		s.exit = Exit{Message: "switched to " + msg.req.Name}
		return s, tea.Quit
	case app.ThenWorktree:
		s.exit = Exit{CdPath: msg.path, Message: "created worktree " + msg.path}
		return s, tea.Quit
	}
	s.newBranch = nil
	s.status = okStatus("created " + msg.req.Name)
	return s.refresh()
}

// onDeleted reports deletion results and reloads.
func (s screen) onDeleted(msg deletedMsg) (tea.Model, tea.Cmd) {
	if len(msg.failures) > 0 {
		s.status = errStatus("delete failed: " + strings.Join(msg.failures, "; "))
	} else {
		s.status = okStatus("deleted " + strings.Join(msg.deleted, ", "))
	}
	return s.refresh()
}

// onNewBranchKey handles keys while the new-branch dialog is open.
func (s screen) onNewBranchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := *s.newBranch
	var cmd tea.Cmd
	switch msg.String() {
	case "esc":
		s.newBranch = nil
		return s, nil
	case "enter":
		if d.submitting {
			return s, nil
		}
		req, err := d.request()
		if err != nil {
			d.err = err.Error()
			s.newBranch = &d
			return s, nil
		}
		d.err = ""
		d.submitting = true
		cmd = createCmd(s.actions, s.snap, req)
	case "tab":
		d, cmd = d.cycleBase()
	case "left":
		d = d.cycleThen(-1)
	case "right":
		d = d.cycleThen(1)
	default:
		d, cmd = d.updateInputs(msg)
	}
	s.newBranch = &d
	return s, cmd
}

// openNewBranch opens the new-branch dialog.
func (s screen) openNewBranch() (screen, tea.Cmd) {
	highlighted := ""
	if b, ok := s.highlighted(); ok {
		highlighted = b.Name
	}
	d, cmd := newNewBranchDialog(s.bases, highlighted, s.snap.Prefix, s.snap.MainPath)
	s.newBranch = &d
	return s, cmd
}

// request builds the CreateRequest the dialog describes.
func (d newBranchDialog) request() (app.CreateRequest, error) {
	name := strings.TrimSpace(d.name.Value())
	if name == "" {
		return app.CreateRequest{}, errors.New("enter a branch name")
	}
	base := d.choices[d.choice]
	if base == customChoice {
		base = strings.TrimSpace(d.custom.Value())
		if base == "" {
			return app.CreateRequest{}, errors.New("enter a ref to branch from")
		}
	}
	return app.CreateRequest{Name: name, Base: base, Then: d.then}, nil
}

// undeletableText explains why none of targets can be deleted.
func undeletableText(targets []model.Branch) string {
	if len(targets) != 1 {
		return "nothing to delete"
	}
	risk := model.Assess(targets[0])
	if len(risk.Reasons) == 0 {
		return targets[0].Name + " cannot be deleted"
	}
	return targets[0].Name + " cannot be deleted: " + strings.Join(risk.Reasons, "; ")
}

// updateInputs forwards msg to both text inputs; only the focused one
// reacts.
func (d newBranchDialog) updateInputs(msg tea.Msg) (newBranchDialog, tea.Cmd) {
	var nameCmd, customCmd tea.Cmd
	d.name, nameCmd = d.name.Update(msg)
	d.custom, customCmd = d.custom.Update(msg)
	return d, tea.Batch(nameCmd, customCmd)
}

// view renders the delete dialog.
func (d confirmDialog) view() string {
	lines := []string{d.title}
	for _, l := range d.lines {
		lines = append(lines, "  "+l)
	}
	return strings.Join(append(lines, ui.Dim("y delete · n skip · esc cancel")), "\n")
}

// view renders the new-branch dialog.
func (d newBranchDialog) view() string {
	from := ui.Cyan(d.choices[d.choice])
	if d.choices[d.choice] == customChoice {
		from += " " + d.custom.View()
	}
	var others []string
	for i := 1; i < len(d.choices); i++ {
		others = append(others, "▸ "+d.choices[(d.choice+i)%len(d.choices)])
	}
	radios := make([]string, len(thenLabels))
	for i, label := range thenLabels {
		radios[i] = "( ) " + label
		if app.Then(i) == d.then {
			radios[i] = ui.Cyan("(•) " + label)
		}
	}
	lines := []string{
		ui.Cyan("New branch"),
		"  name  " + d.name.View(),
		"  from  " + from + ui.Dim("  tab: change"),
		"        " + ui.Dim(strings.Join(others, " ")),
		"  then  " + strings.Join(radios, "  ") + ui.Dim("  ←/→: change"),
	}
	if d.then == app.ThenWorktree && d.mainPath != "" {
		lines = append(lines, "        "+ui.Dim("worktree path: "+model.WorktreePath(d.mainPath, strings.TrimSpace(d.name.Value()))))
	}
	if d.err != "" {
		lines = append(lines, "  "+ui.Red(d.err))
	}
	return strings.Join(append(lines, ui.Dim("  enter create · esc cancel")), "\n")
}
