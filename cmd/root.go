// Package cmd implements the kk-branches command.
package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/localshred/koolkit/pkg/run"
	"github.com/localshred/koolkit/pkg/ui"

	"github.com/localshred/kk-branches/internal/app"
	"github.com/localshred/kk-branches/internal/gh"
	"github.com/localshred/kk-branches/internal/git"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/kk-branches/internal/syncer"
	"github.com/localshred/kk-branches/internal/tui"
)

// branchWidth is the rune width branch names are truncated to in the
// --print table, matching the TUI's own column.
const branchWidth = 40

// riskWords renders each risk level for the --print table's last
// column, matching the TUI's own labels.
var riskWords = map[model.Level]string{
	model.LevelBlocked: ui.Red("blocked"),
	model.LevelConfirm: ui.Yellow("confirm"),
	model.LevelNone:    "",
	model.LevelPending: "…",
	model.LevelSafe:    ui.Green("safe"),
}

// Execute runs the root command and returns a process exit code.
func Execute() int {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kk-branches:", err)
		return 1
	}
	return 0
}

// NewRoot builds the kk-branches cobra command tree.
func NewRoot() *cobra.Command {
	return newRoot(run.Default)
}

// actions adapts app.App and syncer.Engine to satisfy tui.Actions.
type actions struct {
	app app.App
}

func (a actions) AddWorktree(s model.Snapshot, branch string) (string, error) {
	return a.app.AddWorktree(s, branch)
}

func (a actions) Bases(s model.Snapshot) []string {
	return a.app.Bases(s)
}

func (a actions) Checkout(s model.Snapshot, b model.Branch) error {
	return a.app.Checkout(s, b)
}

func (a actions) Create(s model.Snapshot, req app.CreateRequest) (string, error) {
	return a.app.Create(s, req)
}

func (a actions) Delete(b model.Branch) error {
	return a.app.Delete(b)
}

func (a actions) Load() (model.Snapshot, error) {
	return a.app.Load()
}

func (a actions) LoadPRs(s model.Snapshot) (model.Snapshot, error) {
	return a.app.LoadPRs(s)
}

func (a actions) Open(url string) error {
	return a.app.Open(url)
}

func (a actions) RemoveWorktree(b model.Branch) error {
	return a.app.RemoveWorktree(b)
}

func (a actions) Sync(branches []model.Branch, emit func(syncer.Event)) error {
	return syncer.Engine{Repo: a.app.Repo}.Run(branches, emit)
}

// newRoot builds the kk-branches cobra command tree over r, so tests
// can supply a runner that stubs gh while still exercising a real git
// repository.
func newRoot(r run.Runner) *cobra.Command {
	var printOnly bool

	cmd := &cobra.Command{
		Use:           "branches",
		Short:         "Interactive manager for local branches, their PRs, and worktrees",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			if err := run.Require(r, "git", "install git: https://git-scm.com"); err != nil {
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			repo, err := git.Open(r, cwd)
			if err != nil {
				return err
			}

			a := app.App{Repo: repo, GH: gh.Client{Run: r}, Now: time.Now}

			if printOnly {
				return runPrint(os.Stdout, os.Stderr, a)
			}

			exit, err := tui.Run(actions{app: a})
			if err != nil {
				return err
			}
			if exit.Message != "" {
				fmt.Println(exit.Message)
			}
			if exit.CdPath != "" {
				fmt.Println("cd " + exit.CdPath)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&printOnly, "print", false,
		"print the branch table once and exit, without the interactive UI")

	return cmd
}

// printHeader returns the dimmed column header row for the --print
// table, matching the TUI's own header labels.
func printHeader() []string {
	labels := []string{"BRANCH", "ORIGIN", "PR", "WORKTREE", "AGE", "SAFE DELETION?"}
	header := make([]string, len(labels))
	for i, label := range labels {
		if label == "" {
			continue
		}
		header[i] = ui.Dim(label)
	}
	return header
}

// printRow renders one branch's cells for the --print table, matching
// the TUI's own row layout.
func printRow(b model.Branch, mainPath string) []string {
	pr := model.PRCell(b)
	if b.Trunk {
		pr = ui.Dim("trunk")
	}
	return []string{
		model.BranchLabel(b, branchWidth),
		model.OriginCell(b.Upstream),
		pr,
		worktreeCell(b, mainPath),
		model.AgeCell(b, time.Now()),
		riskWords[model.Assess(b).Level],
	}
}

// runPrint loads the branch snapshot and its pull requests, then
// prints the table once to w and exits successfully even when the PR
// lookup failed — in that case a dim note is written to errw instead
// of failing the command outright.
func runPrint(w, errw io.Writer, a app.App) error {
	snap, err := a.Load()
	if err != nil {
		return err
	}

	snap, err = a.LoadPRs(snap)
	if err != nil {
		fmt.Fprintln(errw, ui.Dim("PR lookup failed: "+err.Error()))
	}

	rows := make([][]string, 0, len(snap.Branches)+1)
	rows = append(rows, printHeader())
	for _, b := range snap.Branches {
		rows = append(rows, printRow(b, snap.MainPath))
	}
	ui.PrintTable(w, rows)
	return nil
}

// worktreeCell renders where b is checked out, relative to the main
// checkout's parent directory, matching the TUI's own column.
func worktreeCell(b model.Branch, mainPath string) string {
	w := b.Worktree
	if w == nil {
		return ui.Dim("—")
	}
	cell := "(main checkout)"
	if !w.Main {
		cell = w.Path
		if mainPath != "" {
			if rel, err := filepath.Rel(filepath.Dir(mainPath), w.Path); err == nil {
				cell = rel
			}
		}
	}
	if w.Dirty {
		cell += " " + ui.Red("✎ dirty")
	}
	return cell
}
