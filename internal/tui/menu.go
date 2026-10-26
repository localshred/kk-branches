package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/localshred/koolkit/pkg/ui"
)

// menus maps each prefix key to the group of commands it opens.
var menus = map[string]menu{
	"b": {title: "branch", items: []menuItem{
		{key: "c", label: "checkout in main repo", run: screen.checkout},
		{key: "d", label: "delete selected rows, or the highlighted one", run: screen.deleteTargets},
		{key: "n", label: "new branch", run: screen.openNewBranch},
	}},
	"w": {title: "worktree", items: []menuItem{
		{key: "a", label: "add a worktree for the highlighted row", run: screen.addWorktree},
		{key: "d", label: "remove the highlighted row's worktree", run: screen.removeWorktree},
		{key: "g", label: "go: quit and print cd to the highlighted row's worktree", run: screen.goWorktree},
	}},
}

// menu is a prefix key's group of commands, shown below the table
// until one of its keys is pressed.
type menu struct {
	title string
	items []menuItem
}

// menuItem is one command in a menu.
type menuItem struct {
	key   string
	label string
	run   func(screen) (screen, tea.Cmd)
}

// onMenuKey closes the open menu and runs its command for k; esc, or
// any key the menu doesn't have, only closes it.
func (s screen) onMenuKey(k string) (tea.Model, tea.Cmd) {
	m := menus[s.menu]
	s.menu = ""
	for _, it := range m.items {
		if it.key == k {
			return it.run(s)
		}
	}
	return s, nil
}

// openMenu returns a key handler that opens the menu for prefix.
func openMenu(prefix string) func(screen) (screen, tea.Cmd) {
	return func(s screen) (screen, tea.Cmd) {
		s.menu = prefix
		return s, nil
	}
}

// view renders m's title and keys.
func (m menu) view() string {
	lines := []string{ui.Cyan(m.title)}
	for _, it := range m.items {
		lines = append(lines, "  "+it.key+"  "+it.label)
	}
	return strings.Join(append(lines, ui.Dim("esc cancel")), "\n")
}
