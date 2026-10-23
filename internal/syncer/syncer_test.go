package syncer

import (
	"errors"
	"strings"
	"testing"

	"github.com/localshred/kk-branches/internal/git"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/koolkit/pkg/run"
)

// joinedCalls returns the space-joined form of every call recorded in
// calls, matching run.Fake's own key format.
func joinedCalls(calls [][]string) []string {
	out := make([]string, len(calls))
	for i, call := range calls {
		out[i] = strings.Join(call, " ")
	}
	return out
}

func containsCall(calls [][]string, want string) bool {
	for _, got := range joinedCalls(calls) {
		if got == want {
			return true
		}
	}
	return false
}

func containsSubstring(calls [][]string, sub string) bool {
	for _, got := range joinedCalls(calls) {
		if strings.Contains(got, sub) {
			return true
		}
	}
	return false
}

func collect(events *[]Event) func(Event) {
	return func(e Event) { *events = append(*events, e) }
}

func TestSyncFetchFailureStops(t *testing.T) {
	fake := &run.Fake{Errs: map[string]error{
		"git -C /r/app fetch origin --prune": errors.New("exit status 1"),
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	err := e.Run(nil, collect(&events))
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	if len(events) != 2 || events[0].Status != StatusFetching || events[1].Status != StatusFailed {
		t.Fatalf("events = %+v, want [Fetching Failed]", events)
	}
}

func TestSyncRestoresDetachedHead(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                     {},
			"git -C /r/app rev-parse HEAD":                           {Stdout: "abc123\n"},
			"git -C /r/app status --porcelain":                       {},
			"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "3\n"},
			"git -C /r/app checkout --quiet feature":                 {},
			"git -C /r/app rebase origin/feature --quiet":            {},
			"git -C /r/app checkout --quiet abc123":                  {},
		},
		Errs: map[string]error{
			"git -C /r/app symbolic-ref --short -q HEAD":                    errors.New("exit status 1"),
			"git -C /r/app merge-base --is-ancestor feature origin/feature": errors.New("exit status 1"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	calls := fake.Calls
	last := calls[len(calls)-1]
	want := []string{"git", "-C", "/r/app", "checkout", "--quiet", "abc123"}
	if strings.Join(last, " ") != strings.Join(want, " ") {
		t.Errorf("last call = %v, want %v", last, want)
	}
}

func TestSyncSkipsDirtyWorktree(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":                     {},
		"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
		"git -C /r/app status --porcelain":                       {},
		"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
		"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
		"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
		"git -C /r/app-wt-feature status --porcelain":            {Stdout: "M file\n"},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{
			Name:     "feature",
			Upstream: &model.Upstream{Ref: "origin/feature"},
			Worktree: &model.Worktree{Path: "/r/app-wt-feature", Current: false},
		},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusSkipped {
			found = true
			if ev.Detail != "worktree dirty" {
				t.Errorf("Detail = %q, want %q", ev.Detail, "worktree dirty")
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Skipped event for feature", events)
	}

	if containsSubstring(fake.Calls, "stash") {
		t.Errorf("Calls = %v, want no stash call", joinedCalls(fake.Calls))
	}
	if containsSubstring(fake.Calls, "/r/app-wt-feature rebase") {
		t.Errorf("Calls = %v, want no rebase call in the dirty worktree", joinedCalls(fake.Calls))
	}
}

func TestSyncFastForwardsWithoutCheckout(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":                            {},
		"git -C /r/app symbolic-ref --short -q HEAD":                    {Stdout: "main\n"},
		"git -C /r/app status --porcelain":                              {},
		"git -C /r/app rev-parse feature":                               {Stdout: "aaa\n"},
		"git -C /r/app rev-parse origin/feature":                        {Stdout: "bbb\n"},
		"git -C /r/app rev-list --count feature..origin/feature":        {Stdout: "2\n"},
		"git -C /r/app merge-base --is-ancestor feature origin/feature": {},
		"git -C /r/app update-ref refs/heads/feature bbb aaa":           {},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if containsSubstring(fake.Calls, "checkout") {
		t.Errorf("Calls = %v, want no checkout call", joinedCalls(fake.Calls))
	}
	if !containsCall(fake.Calls, "git -C /r/app update-ref refs/heads/feature bbb aaa") {
		t.Errorf("Calls = %v, want update-ref call", joinedCalls(fake.Calls))
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusRebased {
			found = true
			if ev.Detail != "+2" {
				t.Errorf("Detail = %q, want +2", ev.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Rebased event for feature", events)
	}
}

func TestSyncRebasesInOtherWorktree(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":                     {},
		"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
		"git -C /r/app status --porcelain":                       {},
		"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
		"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
		"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "4\n"},
		"git -C /r/app-wt-feature status --porcelain":            {},
		"git -C /r/app-wt-feature rebase origin/feature --quiet": {},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{
			Name:     "feature",
			Upstream: &model.Upstream{Ref: "origin/feature"},
			Worktree: &model.Worktree{Path: "/r/app-wt-feature", Current: false},
		},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, "git -C /r/app-wt-feature rebase origin/feature --quiet") {
		t.Errorf("Calls = %v, want a rebase call in /r/app-wt-feature", joinedCalls(fake.Calls))
	}
	if containsSubstring(fake.Calls, "/r/app checkout") {
		t.Errorf("Calls = %v, want no checkout call against /r/app", joinedCalls(fake.Calls))
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusRebased {
			found = true
			if ev.Detail != "+4" {
				t.Errorf("Detail = %q, want +4", ev.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Rebased event for feature", events)
	}
}

func TestSyncOtherWorktreeConflictAborts(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                     {},
			"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                       {},
			"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
			"git -C /r/app-wt-feature status --porcelain":            {},
			"git -C /r/app-wt-feature rebase --abort":                {},
		},
		Errs: map[string]error{
			"git -C /r/app-wt-feature rebase origin/feature --quiet": errors.New("exit status 1"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{
			Name:     "feature",
			Upstream: &model.Upstream{Ref: "origin/feature"},
			Worktree: &model.Worktree{Path: "/r/app-wt-feature", Current: false},
		},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, "git -C /r/app-wt-feature rebase --abort") {
		t.Errorf("Calls = %v, want a rebase --abort call in /r/app-wt-feature", joinedCalls(fake.Calls))
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusConflict {
			found = true
			if ev.Detail != "rebase conflict, aborted" {
				t.Errorf("Detail = %q, want %q", ev.Detail, "rebase conflict, aborted")
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Conflict event for feature", events)
	}
}

func TestSyncOtherWorktreeAbortFails(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                     {},
			"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                       {},
			"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
			"git -C /r/app-wt-feature status --porcelain":            {},
		},
		Errs: map[string]error{
			"git -C /r/app-wt-feature rebase origin/feature --quiet": errors.New("exit status 1"),
			"git -C /r/app-wt-feature rebase --abort":                errors.New("exit status 128: no rebase in progress"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{
			Name:     "feature",
			Upstream: &model.Upstream{Ref: "origin/feature"},
			Worktree: &model.Worktree{Path: "/r/app-wt-feature", Current: false},
		},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" {
			if ev.Status == StatusConflict {
				t.Errorf("events contains Conflict, want Failed since abort itself failed: %+v", events)
			}
			if ev.Status == StatusFailed {
				found = true
				if !strings.Contains(ev.Detail, "abort failed") {
					t.Errorf("Detail = %q, want it to mention the abort failure", ev.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Failed event for feature", events)
	}
}

func TestSyncConflictAborts(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                     {},
			"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                       {},
			"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
			"git -C /r/app checkout --quiet feature":                 {},
			"git -C /r/app rebase --abort":                           {},
			"git -C /r/app checkout --quiet main":                    {},
		},
		Errs: map[string]error{
			"git -C /r/app merge-base --is-ancestor feature origin/feature": errors.New("exit status 1"),
			"git -C /r/app rebase origin/feature --quiet":                   errors.New("exit status 1"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusConflict {
			found = true
			if ev.Detail != "rebase conflict, aborted" {
				t.Errorf("Detail = %q, want %q", ev.Detail, "rebase conflict, aborted")
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Conflict event for feature", events)
	}
	if !containsCall(fake.Calls, "git -C /r/app rebase --abort") {
		t.Errorf("Calls = %v, want a rebase --abort call", joinedCalls(fake.Calls))
	}
	if !containsCall(fake.Calls, "git -C /r/app checkout --quiet main") {
		t.Errorf("Calls = %v, want HEAD restored to main", joinedCalls(fake.Calls))
	}
}

func TestSyncConflictAbortFails(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                     {},
			"git -C /r/app symbolic-ref --short -q HEAD":             {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                       {},
			"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
			"git -C /r/app checkout --quiet feature":                 {},
		},
		Errs: map[string]error{
			"git -C /r/app merge-base --is-ancestor feature origin/feature": errors.New("exit status 1"),
			"git -C /r/app rebase origin/feature --quiet":                   errors.New("exit status 1"),
			"git -C /r/app rebase --abort":                                  errors.New("exit status 128: no rebase in progress"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" {
			if ev.Status == StatusConflict {
				t.Errorf("events contains Conflict, want Failed since abort itself failed: %+v", events)
			}
			if ev.Status == StatusFailed {
				found = true
				if !strings.Contains(ev.Detail, "abort failed") {
					t.Errorf("Detail = %q, want it to mention the abort failure", ev.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a Failed event for feature", events)
	}
}

func TestSyncStashesAndPops(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":                                            {},
		"git -C /r/app symbolic-ref --short -q HEAD":                                    {Stdout: "main\n"},
		"git -C /r/app status --porcelain":                                              {Stdout: "M file\n"},
		"git -C /r/app stash push --include-untracked --quiet -m kk branches autostash": {},
		"git -C /r/app stash pop --quiet":                                               {},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, "git -C /r/app stash push --include-untracked --quiet -m kk branches autostash") {
		t.Errorf("Calls = %v, want a stash push call", joinedCalls(fake.Calls))
	}
	if !containsCall(fake.Calls, "git -C /r/app stash pop --quiet") {
		t.Errorf("Calls = %v, want a stash pop call", joinedCalls(fake.Calls))
	}
	for _, ev := range events {
		if ev.Status == StatusStashConflict {
			t.Errorf("events contains StashConflict, want none: %+v", events)
		}
	}
}

func TestSyncStashPopConflict(t *testing.T) {
	fake := &run.Fake{
		Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                                            {},
			"git -C /r/app symbolic-ref --short -q HEAD":                                    {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                                              {Stdout: "M file\n"},
			"git -C /r/app stash push --include-untracked --quiet -m kk branches autostash": {},
		},
		Errs: map[string]error{
			"git -C /r/app stash pop --quiet": errors.New("exit status 1"),
		},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Status == StatusStashConflict {
			found = true
			want := "stash pop conflicted; resolve manually (git stash list)"
			if ev.Detail != want {
				t.Errorf("Detail = %q, want %q", ev.Detail, want)
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a StashConflict event", events)
	}
}

func TestSyncSkipsGoneAndLocalOnly(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":         {},
		"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
		"git -C /r/app status --porcelain":           {},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "spike"},
		{Name: "old", Upstream: &model.Upstream{Ref: "origin/old", Gone: true}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, ev := range events {
		if ev.Status == StatusRunning {
			t.Errorf("events contains Running, want none: %+v", events)
		}
	}
}

func TestSyncUpToDate(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":         {},
		"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
		"git -C /r/app status --porcelain":           {},
		"git -C /r/app rev-parse feature":            {Stdout: "same\n"},
		"git -C /r/app rev-parse origin/feature":     {Stdout: "same\n"},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Branch == "feature" && ev.Status == StatusUpToDate {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want an UpToDate event for feature", events)
	}
	if containsSubstring(fake.Calls, "rev-list") {
		t.Errorf("Calls = %v, want no rev-list call", joinedCalls(fake.Calls))
	}
}
