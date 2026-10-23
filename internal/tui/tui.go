// Package tui implements kk-branches' interactive Bubble Tea interface:
// a table of local branches with their origin status, pull request,
// worktree, and deletion risk, plus dialogs for deleting, creating, and
// syncing branches. Every repository operation goes through the Actions
// interface, so the screen can be driven in tests without git, GitHub,
// or a terminal.
package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/localshred/kk-branches/internal/app"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/kk-branches/internal/syncer"
)

// Actions is everything the TUI asks of the application layer. Each
// method may block on git or GitHub, so the screen only calls them
// from Bubble Tea commands, never from Update or View.
type Actions interface {
	Load() (model.Snapshot, error)
	LoadPRs(model.Snapshot) (model.Snapshot, error)
	Bases(model.Snapshot) []string
	Create(model.Snapshot, app.CreateRequest) (string, error)
	AddWorktree(model.Snapshot, string) (string, error)
	RemoveWorktree(model.Branch) error
	Delete(model.Branch) error
	Open(string) error
	Sync(branches []model.Branch, emit func(syncer.Event)) error
}

// Exit describes what the caller should do once the TUI has closed.
type Exit struct {
	CdPath  string // set after creating a worktree
	Message string // printed after the TUI exits
}

// Run shows the branch table on the terminal until the user quits,
// returning any follow-up for the caller to print.
func Run(a Actions) (Exit, error) {
	final, err := tea.NewProgram(newScreen(a, time.Now)).Run()
	if err != nil {
		return Exit{}, err
	}
	s, _ := final.(screen)
	return s.exit, nil
}
