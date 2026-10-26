package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/kk-branches/internal/syncer"
	"github.com/localshred/koolkit/pkg/ui"
)

// ansiReset ends every color sequence the table cells emit.
const ansiReset = "\033[0m"

// branchWidth is the rune width branch names are truncated to.
const branchWidth = 40

// columnGap separates table columns.
const columnGap = "  "

// cursorBackground is the 256-color background painted behind the
// cursor row.
const cursorBackground = "\033[48;5;236m"

// interruptedText is the Exit message after a force quit during a
// sync.
const interruptedText = "sync interrupted; HEAD and the autostash may need restoring (check git status and git stash list)"

// quitDeferredText is the status shown when a quit waits for a running
// sync.
const quitDeferredText = "quitting after sync finishes — press ctrl+c again to force quit"

// syncRunningText is the status shown when a key is disabled by a
// running sync.
const syncRunningText = "sync running"

// actionKeys are the keys that change repository state or start work;
// they are disabled while a sync is running.
var actionKeys = map[string]func(screen) (screen, tea.Cmd){
	"A":     screen.clearSelection,
	"a":     screen.selectSafe,
	"b":     openMenu("b"),
	"o":     screen.openPR,
	"r":     screen.refresh,
	"s":     screen.startSync,
	"space": screen.toggleSelect,
	"w":     openMenu("w"),
}

// columnHeaders labels the table columns after the select glyph.
var columnHeaders = []string{"BRANCH", "ORIGIN", "PR", "WORKTREE", "AGE", "SAFE DELETION?"}

// helpText lists every key, shown by the ? overlay.
var helpText = []string{
	"↑/k ↓/j     move",
	"space       select / unselect (safe and confirm rows)",
	"a / A       select every safe row in view / clear the selection",
	"tab         next filter preset (shift+tab: previous)",
	"/           filter by name (enter keeps, esc clears)",
	"i           explain the highlighted row's deletion risk",
	"o           open the highlighted row's PR",
	"y           copy the highlighted branch name",
	"s           sync all branches with origin",
	"r           reload",
	"b c         branch: check out in the main checkout",
	"b d         branch: delete selection, or the highlighted row",
	"b n         branch: new",
	"w a         worktree: add one for the highlighted row",
	"w d         worktree: remove the highlighted row's",
	"w g         worktree: quit and print cd to the highlighted row's",
	"?           toggle this help",
	"q / ctrl+c  quit",
}

// infoLevels names each risk level in the info panel.
var infoLevels = map[model.Level]string{
	model.LevelBlocked: ui.Red("blocked"),
	model.LevelConfirm: ui.Yellow("confirm"),
	model.LevelNone:    ui.Dim("trunk"),
	model.LevelPending: ui.Dim("checking PR…"),
	model.LevelSafe:    ui.Green("safe"),
}

// navKeys are the keys that stay available while a sync is running.
var navKeys = map[string]func(screen) (screen, tea.Cmd){
	"/":         screen.focusFilter,
	"?":         screen.toggleHelp,
	"down":      screen.moveDown,
	"i":         screen.toggleInfo,
	"j":         screen.moveDown,
	"k":         screen.moveUp,
	"q":         screen.quit,
	"shift+tab": screen.prevPreset,
	"tab":       screen.nextPreset,
	"up":        screen.moveUp,
	"y":         screen.copyName,
}

// riskWords renders each risk level for the table's last column.
var riskWords = map[model.Level]string{
	model.LevelBlocked: ui.Red("blocked"),
	model.LevelConfirm: ui.Yellow("confirm"),
	model.LevelNone:    "",
	model.LevelPending: "…",
	model.LevelSafe:    ui.Green("safe"),
}

// Init starts the initial snapshot load.
func (s screen) Init() tea.Cmd {
	return loadCmd(s.actions, s.gen)
}

// Update applies msg to the screen.
func (s screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case actionMsg:
		return s.onAction(msg)
	case createdMsg:
		return s.onCreated(msg)
	case deletedMsg:
		return s.onDeleted(msg)
	case loadedMsg:
		return s.onLoaded(msg)
	case prsLoadedMsg:
		return s.onPRsLoaded(msg), nil
	case spinner.TickMsg:
		return s.onSpinnerTick(msg)
	case syncFinishedMsg:
		return s.onSyncFinished(msg)
	case syncer.Event:
		return s.onSyncEvent(msg)
	case tea.KeyPressMsg:
		return s.onKey(msg)
	case tea.WindowSizeMsg:
		s.height, s.width = msg.Height, msg.Width
		return s, nil
	default:
		return s.updateInputs(msg)
	}
}

// View renders the screen in the terminal's alternate buffer.
func (s screen) View() tea.View {
	v := tea.NewView(s.render())
	v.AltScreen = true
	return v
}

// actionCmd runs fn and reports ok on success, reloading afterwards
// when reload is set.
func actionCmd(fn func() error, ok string, reload bool) tea.Cmd {
	return func() tea.Msg {
		return actionMsg{err: fn(), ok: ok, reload: reload}
	}
}

// actionMsg reports the outcome of a single-branch action.
type actionMsg struct {
	err    error
	ok     string
	reload bool
}

// addWorktree adds a worktree for the highlighted branch.
func (s screen) addWorktree() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	a, snap := s.actions, s.snap
	return s, func() tea.Msg {
		path, err := a.AddWorktree(snap, b.Name)
		return actionMsg{err: err, ok: "created worktree " + path, reload: true}
	}
}

// checkout checks the highlighted branch out in the main checkout.
func (s screen) checkout() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	a, snap := s.actions, s.snap
	return s, actionCmd(func() error { return a.Checkout(snap, b) }, "checked out "+b.Name+" in "+snap.MainPath, true)
}

// alignColumns pads every cell but the last in each row to its
// column's widest visible width, measured with ui.VisibleLen so
// hyperlinks and colors don't count.
func alignColumns(rows [][]string) []string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], ui.VisibleLen(cell))
		}
	}
	lines := make([]string, len(rows))
	for r, row := range rows {
		var sb strings.Builder
		for i, cell := range row {
			sb.WriteString(cell)
			if i < len(row)-1 {
				sb.WriteString(strings.Repeat(" ", widths[i]-ui.VisibleLen(cell)) + columnGap)
			}
		}
		lines[r] = strings.TrimRight(sb.String(), " ")
	}
	return lines
}

// clampCursor keeps the cursor on a visible row.
func (s screen) clampCursor() screen {
	n := len(s.rows())
	if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	return s
}

// clearSelection unselects every row.
func (s screen) clearSelection() (screen, tea.Cmd) {
	s.selected = map[string]bool{}
	return s, nil
}

// copyName copies the highlighted branch name to the system clipboard
// through the terminal (OSC 52).
func (s screen) copyName() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	s.status = okStatus("copied " + b.Name)
	return s, tea.SetClipboard(b.Name)
}

// copyResults returns a copy of results, so a screen value never
// shares its sync results with an earlier one.
func copyResults(results map[string]string) map[string]string {
	out := make(map[string]string, len(results)+1)
	for k, v := range results {
		out[k] = v
	}
	return out
}

// copySelection returns a copy of sel, so a screen value never shares
// its selection map with an earlier one.
func copySelection(sel map[string]bool) map[string]bool {
	out := make(map[string]bool, len(sel)+1)
	for k, v := range sel {
		out[k] = v
	}
	return out
}

// errStatus builds an error status line.
func errStatus(text string) status { return status{err: true, text: text} }

// focusFilter starts editing the text filter.
func (s screen) focusFilter() (screen, tea.Cmd) {
	return s, s.filter.Focus()
}

// goWorktree quits, leaving the caller to print a cd to the
// highlighted row's worktree.
func (s screen) goWorktree() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	if b.Worktree == nil {
		s.status = errStatus(b.Name + " has no worktree")
		return s, nil
	}
	s.exit = Exit{CdPath: b.Worktree.Path}
	return s, tea.Quit
}

// highlightRow paints cursorBackground behind line out to width
// columns, re-applying it after each reset inside the line's cells.
func highlightRow(line string, width int) string {
	pad := strings.Repeat(" ", max(0, width-ui.VisibleLen(line)))
	return cursorBackground + strings.ReplaceAll(line, ansiReset, ansiReset+cursorBackground) + pad + ansiReset
}

// highlighted returns the branch under the cursor.
func (s screen) highlighted() (model.Branch, bool) {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return model.Branch{}, false
	}
	return rows[s.cursor], true
}

// infoReasons lists why b has its risk level, for the info panel.
func infoReasons(b model.Branch, risk model.Risk) []string {
	switch risk.Level {
	case model.LevelNone:
		return []string{"trunks are never deleted"}
	case model.LevelPending:
		return []string{"PR lookup still running"}
	case model.LevelSafe:
		return []string{"PR #" + strconv.Itoa(b.PR.Number) + " merged and contains the branch tip"}
	}
	return risk.Reasons
}

// interrupt handles ctrl+c: it quits like q, except that a second
// ctrl+c while a quit is already waiting on a sync force quits, so a
// hung sync cannot trap the user.
func (s screen) interrupt() (screen, tea.Cmd) {
	if s.syncing && s.quitAfterSync {
		s.exit = Exit{Message: interruptedText}
		return s, tea.Quit
	}
	return s.quit()
}

// lineCount counts the terminal lines in parts once joined by
// newlines.
func lineCount(parts []string) int {
	return strings.Count(strings.Join(parts, "\n"), "\n") + 1
}

// loadCmd runs Actions.Load and Actions.Bases for generation gen.
func loadCmd(a Actions, gen int) tea.Cmd {
	return func() tea.Msg {
		snap, err := a.Load()
		if err != nil {
			return loadedMsg{err: err, gen: gen}
		}
		return loadedMsg{bases: a.Bases(snap), gen: gen, snap: snap}
	}
}

// loadPRsCmd runs Actions.LoadPRs on snap for generation gen.
func loadPRsCmd(a Actions, snap model.Snapshot, gen int) tea.Cmd {
	return func() tea.Msg {
		out, err := a.LoadPRs(snap)
		return prsLoadedMsg{err: err, gen: gen, snap: out}
	}
}

// loadedMsg carries the result of Actions.Load, plus the branch-creation
// bases for the new snapshot.
type loadedMsg struct {
	bases []string
	err   error
	gen   int
	snap  model.Snapshot
}

// moveDown moves the cursor one row down.
func (s screen) moveDown() (screen, tea.Cmd) {
	s.cursor++
	return s.clampCursor(), nil
}

// moveUp moves the cursor one row up.
func (s screen) moveUp() (screen, tea.Cmd) {
	s.cursor--
	return s.clampCursor(), nil
}

// newScreen builds the initial screen over a, reading time from now.
func newScreen(a Actions, now func() time.Time) screen {
	filter := textinput.New()
	filter.Prompt = ""
	return screen{
		actions:  a,
		filter:   filter,
		now:      now,
		selected: map[string]bool{},
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

// nextPreset switches to the next filter preset.
func (s screen) nextPreset() (screen, tea.Cmd) {
	s.preset = (s.preset + 1) % len(model.Presets)
	return s.clampCursor(), nil
}

// okStatus builds a success status line.
func okStatus(text string) status { return status{text: text} }

// onAction applies the outcome of a single-branch action.
func (s screen) onAction(msg actionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		s.status = errStatus(msg.err.Error())
	} else {
		s.status = okStatus(msg.ok)
	}
	if !msg.reload {
		return s, nil
	}
	return s.refresh()
}

// onFilterKey edits the text filter while it has focus.
func (s screen) onFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.filter.SetValue("")
		s.filter.Blur()
		return s.clampCursor(), nil
	case "enter":
		s.filter.Blur()
		return s, nil
	}
	var cmd tea.Cmd
	s.filter, cmd = s.filter.Update(msg)
	return s.clampCursor(), cmd
}

// onHelpKey handles keys while the help overlay is shown.
func (s screen) onHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "?", "esc":
		s.help = false
	case "q":
		return s.quit()
	}
	return s, nil
}

// onInfoKey handles keys while the info panel is shown; moving the
// cursor keeps it open on the new row.
func (s screen) onInfoKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "esc", "i":
		s.info = false
	case "down", "j", "k", "q", "up":
		return navKeys[k](s)
	}
	return s, nil
}

// onKey routes a keypress to the open dialog, the open menu, the text
// filter, the help overlay, the info panel, or the table.
func (s screen) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch {
	case k == "ctrl+c":
		return s.interrupt()
	case s.newBranch != nil:
		return s.onNewBranchKey(msg)
	case s.confirm != nil:
		return s.onConfirmKey(k)
	case s.menu != "":
		return s.onMenuKey(k)
	case s.filter.Focused():
		return s.onFilterKey(msg)
	case s.help:
		return s.onHelpKey(msg)
	case s.info:
		return s.onInfoKey(msg)
	}
	if fn, ok := navKeys[k]; ok {
		return fn(s)
	}
	fn, ok := actionKeys[k]
	if !ok {
		return s, nil
	}
	if s.syncing {
		s.status = errStatus(syncRunningText)
		return s, nil
	}
	return fn(s)
}

// onLoaded installs a freshly loaded snapshot and starts the PR lookup.
func (s screen) onLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != s.gen {
		return s, nil
	}
	if msg.err != nil {
		s.status = errStatus("load failed: " + msg.err.Error())
		return s, nil
	}
	s.bases = msg.bases
	s.selected = map[string]bool{}
	s.snap = msg.snap
	return s.clampCursor(), loadPRsCmd(s.actions, msg.snap, s.gen)
}

// onPRsLoaded installs the snapshot with resolved pull requests.
func (s screen) onPRsLoaded(msg prsLoadedMsg) screen {
	if msg.gen != s.gen {
		return s
	}
	s.snap = msg.snap
	if msg.err != nil {
		s.status = errStatus("PR lookup failed: " + msg.err.Error())
	}
	return s.clampCursor()
}

// onSpinnerTick advances the sync spinner.
func (s screen) onSpinnerTick(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if !s.syncing {
		return s, nil
	}
	var cmd tea.Cmd
	s.spinner, cmd = s.spinner.Update(msg)
	return s, cmd
}

// onSyncEvent records one sync step and waits for the next.
func (s screen) onSyncEvent(e syncer.Event) (tea.Model, tea.Cmd) {
	s.syncDone, s.syncTotal = e.Done, e.Total
	switch {
	case e.Branch != "":
		s.syncResults = copyResults(s.syncResults)
		s.syncResults[e.Branch] = syncCell(e)
	case e.Status == syncer.StatusFailed:
		s.syncFailed = true
		s.status = errStatus("sync failed: " + e.Detail)
	case e.Status == syncer.StatusStashConflict:
		s.syncFailed = true
		s.status = errStatus(e.Detail)
	}
	return s, waitSync(s.syncCh)
}

// onSyncFinished ends sync mode, then reloads or performs a deferred
// quit.
func (s screen) onSyncFinished(msg syncFinishedMsg) (tea.Model, tea.Cmd) {
	s.syncing = false
	s.syncCh = nil
	switch {
	case msg.err != nil:
		s.status = errStatus("sync failed: " + msg.err.Error())
	case !s.syncFailed:
		s.status = okStatus("sync finished")
	}
	if s.quitAfterSync {
		return s, tea.Quit
	}
	s.gen++
	return s, loadCmd(s.actions, s.gen)
}

// openPR opens the highlighted branch's pull request in a browser.
func (s screen) openPR() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	if b.PR == nil {
		s.status = errStatus("no PR for " + b.Name)
		return s, nil
	}
	a, url := s.actions, b.PR.URL
	return s, actionCmd(func() error { return a.Open(url) }, "opened PR #"+strconv.Itoa(b.PR.Number), false)
}

// prevPreset switches to the previous filter preset.
func (s screen) prevPreset() (screen, tea.Cmd) {
	s.preset = (s.preset + len(model.Presets) - 1) % len(model.Presets)
	return s.clampCursor(), nil
}

// prsLoadedMsg carries the result of Actions.LoadPRs.
type prsLoadedMsg struct {
	err  error
	gen  int
	snap model.Snapshot
}

// quit exits the program, or defers exiting until a running sync has
// restored the user's HEAD and stash.
func (s screen) quit() (screen, tea.Cmd) {
	if s.syncing {
		s.quitAfterSync = true
		s.status = okStatus(quitDeferredText)
		return s, nil
	}
	return s, tea.Quit
}

// refresh clears sync results and reloads the snapshot.
func (s screen) refresh() (screen, tea.Cmd) {
	s.gen++
	s.syncResults = nil
	return s, loadCmd(s.actions, s.gen)
}

// relWorktreePath returns path relative to mainPath's parent
// directory, or path unchanged when mainPath is unknown or the two
// are unrelated.
func relWorktreePath(mainPath, path string) string {
	if mainPath == "" {
		return path
	}
	if rel, err := filepath.Rel(filepath.Dir(mainPath), path); err == nil {
		return rel
	}
	return path
}

// removeWorktree removes the highlighted branch's worktree.
func (s screen) removeWorktree() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	if b.Worktree == nil {
		s.status = errStatus(b.Name + " has no worktree")
		return s, nil
	}
	a := s.actions
	return s, actionCmd(func() error { return a.RemoveWorktree(b) }, "removed worktree "+b.Worktree.Path, true)
}

// render draws the whole screen as a string.
func (s screen) render() string {
	top := []string{s.renderHeader(), s.renderFilters()}
	var bottom []string
	switch {
	case s.newBranch != nil:
		bottom = append(bottom, "", s.newBranch.view())
	case s.confirm != nil:
		bottom = append(bottom, "", s.confirm[0].view())
	case s.menu != "":
		bottom = append(bottom, "", menus[s.menu].view())
	case s.info:
		bottom = append(bottom, "", s.renderInfo())
	}
	bottom = append(bottom, s.renderStatus(), s.renderFooter())
	if s.help {
		return strings.Join(append(append(top, "", strings.Join(helpText, "\n"), ""), bottom...), "\n")
	}
	room := 0
	if s.height > 0 {
		room = max(1, s.height-len(top)-1-lineCount(bottom))
	}
	return strings.Join(append(append(top, s.renderTable(room)...), bottom...), "\n")
}

// renderFilters draws the preset tabs with counts and the text filter.
func (s screen) renderFilters() string {
	now, text := s.now(), s.filter.Value()
	tabs := make([]string, len(model.Presets))
	for i, p := range model.Presets {
		label := p.Label() + " " + strconv.Itoa(len(model.Filter(s.snap.Branches, p, text, now)))
		if i == s.preset {
			label = ui.Cyan("[" + label + "]")
		}
		tabs[i] = label
	}
	line := "filter: " + strings.Join(tabs, " · ")
	switch {
	case s.filter.Focused():
		return line + "  / " + s.filter.View()
	case text != "":
		return line + "  / " + text
	default:
		return line + ui.Dim("  / to search")
	}
}

// renderFooter draws the selection count and key hints.
func (s screen) renderFooter() string {
	hints := "space select · a select safe · b branch… · w worktree… · s sync · y copy · i info · o open PR · r reload · ? help · q quit"
	return strconv.Itoa(len(s.selected)) + " selected · " + ui.Dim(hints)
}

// renderHeader draws the repository, trunk, and fetch or sync state.
func (s screen) renderHeader() string {
	trunk := s.snap.Trunk
	if trunk == "" {
		trunk = "unknown"
	}
	state := ui.Dim("↻ never fetched")
	switch {
	case s.syncing:
		state = s.spinner.View() + " syncing " + strconv.Itoa(s.syncDone) + "/" + strconv.Itoa(s.syncTotal)
	case !s.snap.FetchedAt.IsZero():
		state = ui.Dim("↻ fetched " + ui.Humanize(s.now().Sub(s.snap.FetchedAt)) + " ago")
	}
	return ui.Cyan(s.snap.Repo) + "  trunk: " + trunk + "  " + state
}

// renderInfo explains the highlighted row's deletion risk and shows its
// last sync result.
func (s screen) renderInfo() string {
	b, ok := s.highlighted()
	if !ok {
		return ui.Dim("no branch highlighted · i/esc close")
	}
	risk := model.Assess(b)
	lines := []string{ui.Cyan(b.Name), "  safe deletion: " + infoLevels[risk.Level]}
	for _, r := range infoReasons(b, risk) {
		lines = append(lines, "  · "+r)
	}
	if last, ok := s.syncResults[b.Name]; ok {
		lines = append(lines, "  last sync: "+last)
	}
	return strings.Join(append(lines, ui.Dim("i/esc close")), "\n")
}

// renderRow builds the cells for one branch.
func (s screen) renderRow(b model.Branch) []string {
	glyph := "○"
	switch {
	case b.Trunk:
		glyph = ui.Yellow("★")
	case s.selected[b.Name]:
		glyph = ui.Cyan("◉")
	}
	pr := model.PRCell(b)
	if b.Trunk {
		pr = ui.Dim("trunk")
	}
	last, ok := s.syncResults[b.Name]
	if !ok {
		last = riskWords[model.Assess(b).Level]
	}
	return []string{
		glyph,
		model.BranchLabel(b, branchWidth),
		model.OriginCell(b.Upstream),
		pr,
		s.worktreeCell(b),
		model.AgeCell(b, s.now()),
		last,
	}
}

// renderStatus draws the last status message, or a blank line.
func (s screen) renderStatus() string {
	switch {
	case s.status.text == "":
		return ""
	case s.status.err:
		return ui.Red(s.status.text)
	default:
		return ui.Green(s.status.text)
	}
}

// renderTable draws the column headers and as many rows as fit in room
// lines (all of them when room is 0), scrolled to keep the cursor in
// view.
func (s screen) renderTable(room int) []string {
	rows := s.rows()
	cells := make([][]string, 0, len(rows)+1)
	header := append([]string{""}, columnHeaders...)
	for i, h := range header {
		if h != "" {
			header[i] = ui.Dim(h)
		}
	}
	if s.syncResults != nil {
		header[len(header)-1] = ui.Dim("SYNC RESULT")
	}
	cells = append(cells, header)
	for _, b := range rows {
		cells = append(cells, s.renderRow(b))
	}
	lines := alignColumns(cells)
	for i := range lines {
		if i-1 != s.cursor {
			lines[i] = "  " + lines[i]
			continue
		}
		lines[i] = highlightRow(ui.Cyan("▸ ")+lines[i], s.width)
	}
	if room == 0 || len(lines)-1 <= room {
		return lines
	}
	offset := max(0, s.cursor-room+1)
	return append(lines[:1:1], lines[1+offset:1+offset+room]...)
}

// rows returns the branches visible under the current preset and text
// filter.
func (s screen) rows() []model.Branch {
	return model.Filter(s.snap.Branches, model.Presets[s.preset], s.filter.Value(), s.now())
}

// screen is the Bubble Tea model for the branch table.
type screen struct {
	accepted      []model.Branch
	actions       Actions
	bases         []string
	confirm       []confirmDialog
	cursor        int
	exit          Exit
	filter        textinput.Model
	gen           int
	height        int
	help          bool
	info          bool
	menu          string
	newBranch     *newBranchDialog
	now           func() time.Time
	preset        int
	quitAfterSync bool
	selected      map[string]bool
	snap          model.Snapshot
	spinner       spinner.Model
	status        status
	syncCh        chan tea.Msg
	syncDone      int
	syncFailed    bool
	syncResults   map[string]string
	syncTotal     int
	syncing       bool
	width         int
}

// selectSafe selects every Safe row among the visible rows.
func (s screen) selectSafe() (screen, tea.Cmd) {
	s.selected = copySelection(s.selected)
	for _, b := range s.rows() {
		if model.Assess(b).Level == model.LevelSafe {
			s.selected[b.Name] = true
		}
	}
	return s, nil
}

// startSync runs Actions.Sync in a command, streaming its events back
// through a channel that waitSync drains.
func (s screen) startSync() (screen, tea.Cmd) {
	ch := make(chan tea.Msg)
	s.syncCh = ch
	s.syncDone, s.syncTotal = 0, 0
	s.syncFailed = false
	s.syncResults = map[string]string{}
	s.syncing = true
	s.status = status{}
	a, branches := s.actions, s.snap.Branches
	run := func() tea.Msg {
		err := a.Sync(branches, func(e syncer.Event) { ch <- e })
		ch <- syncFinishedMsg{err: err}
		close(ch)
		return nil
	}
	return s, tea.Batch(run, waitSync(ch), s.spinner.Tick)
}

// status is the one-line message above the footer.
type status struct {
	err  bool
	text string
}

// syncCell renders a per-branch sync event for the risk column.
func syncCell(e syncer.Event) string {
	switch e.Status {
	case syncer.StatusRunning:
		return ui.Dim("syncing…")
	case syncer.StatusRebased:
		return ui.Green(strings.TrimSpace("rebased " + e.Detail))
	case syncer.StatusUpToDate:
		return ui.Dim("up to date")
	case syncer.StatusSkipped:
		return ui.Yellow("skipped: " + e.Detail)
	case syncer.StatusConflict:
		return ui.Red("conflict: aborted")
	default:
		return ui.Red("failed: " + e.Detail)
	}
}

// syncFinishedMsg reports that Actions.Sync returned.
type syncFinishedMsg struct{ err error }

// toggleHelp shows or hides the help overlay.
func (s screen) toggleHelp() (screen, tea.Cmd) {
	s.help = !s.help
	return s, nil
}

// toggleInfo shows or hides the info panel for the highlighted row.
func (s screen) toggleInfo() (screen, tea.Cmd) {
	if _, ok := s.highlighted(); ok || s.info {
		s.info = !s.info
	}
	return s, nil
}

// toggleSelect selects or unselects the highlighted row when it is
// Safe or Confirm, explaining why when it is Blocked.
func (s screen) toggleSelect() (screen, tea.Cmd) {
	b, ok := s.highlighted()
	if !ok {
		return s, nil
	}
	risk := model.Assess(b)
	switch risk.Level {
	case model.LevelSafe, model.LevelConfirm:
		s.selected = copySelection(s.selected)
		if s.selected[b.Name] {
			delete(s.selected, b.Name)
		} else {
			s.selected[b.Name] = true
		}
	case model.LevelBlocked:
		s.status = errStatus(b.Name + ": " + strings.Join(risk.Reasons, "; "))
	}
	return s, nil
}

// updateInputs forwards non-key messages, such as cursor blinks, to
// whichever text input is focused.
func (s screen) updateInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case s.newBranch != nil:
		d := *s.newBranch
		d, cmd = d.updateInputs(msg)
		s.newBranch = &d
	case s.filter.Focused():
		s.filter, cmd = s.filter.Update(msg)
	}
	return s, cmd
}

// waitSync returns a command that delivers the next message from a
// running sync, or nil when no sync channel is open.
func waitSync(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

// worktreeCell renders where b is checked out, relative to the main
// checkout's parent directory.
func (s screen) worktreeCell(b model.Branch) string {
	w := b.Worktree
	if w == nil {
		return ui.Dim("—")
	}
	cell := "(main checkout)"
	if !w.Main {
		cell = relWorktreePath(s.snap.MainPath, w.Path)
	}
	if w.Dirty {
		cell += " " + ui.Red("✎ dirty")
	}
	return cell
}
