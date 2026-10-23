package syncer

import (
	"errors"
	"os"
	"path/filepath"
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

// findEvent returns the first event in events with status, and
// whether there was one.
func findEvent(events []Event, status Status) (Event, bool) {
	for _, ev := range events {
		if ev.Status == status {
			return ev, true
		}
	}
	return Event{}, false
}

// step is one scripted answer for a seqFake key.
type step struct {
	res run.Result
	err error
}

// seqFake wraps run.Fake so a key can answer differently on each call:
// a call whose key has steps left in seq consumes the first one, and
// every other call falls through to the wrapped Fake.
type seqFake struct {
	*run.Fake
	seq map[string][]step
}

func (f *seqFake) Run(name string, args ...string) (run.Result, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if queue := f.seq[key]; len(queue) > 0 {
		f.seq[key] = queue[1:]
		f.Calls = append(f.Calls, append([]string{name}, args...))
		return queue[0].res, queue[0].err
	}
	return f.Fake.Run(name, args...)
}

// gitPathKey is the run.Fake key for the in-progress-operation lookup
// in dir.
func gitPathKey(dir string) string {
	return "git -C " + dir + " rev-parse --git-path rebase-merge --git-path rebase-apply " +
		"--git-path MERGE_HEAD --git-path CHERRY_PICK_HEAD --git-path BISECT_LOG"
}

const (
	stashPushKey = "git -C /r/app stash push --include-untracked --quiet -m kk branches autostash"
	stashTipKey  = "git -C /r/app rev-parse -q --verify refs/stash"
)

// noStash is the step for `rev-parse -q --verify refs/stash` when no
// stash exists.
var noStash = step{err: errors.New("exit status 1")}

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
		"git -C /r/app fetch origin --prune":                                                       {},
		"git -C /r/app symbolic-ref --short -q HEAD":                                               {Stdout: "main\n"},
		"git -C /r/app status --porcelain":                                                         {},
		"git -C /r/app rev-parse feature":                                                          {Stdout: "aaa\n"},
		"git -C /r/app rev-parse origin/feature":                                                   {Stdout: "bbb\n"},
		"git -C /r/app rev-list --count feature..origin/feature":                                   {Stdout: "2\n"},
		"git -C /r/app merge-base --is-ancestor feature origin/feature":                            {},
		"git -C /r/app fetch --no-prune --no-write-fetch-head . origin/feature:refs/heads/feature": {},
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
	if !containsCall(fake.Calls, "git -C /r/app fetch --no-prune --no-write-fetch-head . origin/feature:refs/heads/feature") {
		t.Errorf("Calls = %v, want a local fetch into refs/heads/feature", joinedCalls(fake.Calls))
	}
	if containsSubstring(fake.Calls, "update-ref") {
		t.Errorf("Calls = %v, want no update-ref call", joinedCalls(fake.Calls))
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

// TestSyncFastForwardRefusedIsSkipped covers git refusing the local
// fetch, e.g. because the branch was checked out or is being rebased
// in another worktree since the snapshot was taken.
func TestSyncFastForwardRefusedIsSkipped(t *testing.T) {
	ffKey := "git -C /r/app fetch --no-prune --no-write-fetch-head . origin/feature:refs/heads/feature"
	fake := &seqFake{
		Fake: &run.Fake{Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":                            {},
			"git -C /r/app symbolic-ref --short -q HEAD":                    {Stdout: "main\n"},
			"git -C /r/app status --porcelain":                              {},
			"git -C /r/app rev-parse feature":                               {Stdout: "aaa\n"},
			"git -C /r/app rev-parse origin/feature":                        {Stdout: "bbb\n"},
			"git -C /r/app rev-list --count feature..origin/feature":        {Stdout: "2\n"},
			"git -C /r/app merge-base --is-ancestor feature origin/feature": {},
		}},
		seq: map[string][]step{ffKey: {{
			res: run.Result{Stderr: "fatal: refusing to fetch into branch 'refs/heads/feature' checked out at '/r/app-wt-feature'\n"},
			err: errors.New("exit status 128"),
		}}},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	branches := []model.Branch{
		{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
	}

	var events []Event
	if err := e.Run(branches, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	ev, ok := findEvent(events, StatusSkipped)
	if !ok || ev.Branch != "feature" {
		t.Fatalf("events = %+v, want a Skipped event for feature", events)
	}
	want := "refusing to fetch into branch 'refs/heads/feature' checked out at '/r/app-wt-feature'"
	if ev.Detail != want {
		t.Errorf("Detail = %q, want %q", ev.Detail, want)
	}
	if _, failed := findEvent(events, StatusFailed); failed {
		t.Errorf("events = %+v, want no Failed event", events)
	}
}

// TestSyncRefusesInProgressOperation covers each in-progress marker,
// reported both relative to the worktree (as git does in the main
// checkout) and absolute (as it does in a linked worktree).
func TestSyncRefusesInProgressOperation(t *testing.T) {
	markers := []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "BISECT_LOG"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			gitDir := filepath.Join(dir, ".git")
			if err := os.MkdirAll(filepath.Join(gitDir, marker), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			key := gitPathKey(dir)
			fake := &run.Fake{Results: map[string]run.Result{
				key: {Stdout: ".git/rebase-merge\n.git/rebase-apply\n.git/MERGE_HEAD\n" +
					".git/CHERRY_PICK_HEAD\n" + gitDir + "/BISECT_LOG\n"},
			}}
			e := Engine{Repo: git.Repo{Run: fake, Dir: dir}}
			branches := []model.Branch{
				{Name: "feature", Upstream: &model.Upstream{Ref: "origin/feature"}},
			}

			var events []Event
			err := e.Run(branches, collect(&events))
			want := "rebase/merge in progress in this worktree; finish it first"
			if err == nil || err.Error() != want {
				t.Fatalf("Run() error = %v, want %q", err, want)
			}
			ev, ok := findEvent(events, StatusFailed)
			if !ok || ev.Detail != want {
				t.Errorf("events = %+v, want a Failed event with Detail %q", events, want)
			}
			if got := joinedCalls(fake.Calls); len(got) != 1 || got[0] != key {
				t.Errorf("Calls = %v, want only the git-path lookup", got)
			}
		})
	}
}

func TestSyncProceedsWithNoOperationInProgress(t *testing.T) {
	dir := t.TempDir()
	fake := &run.Fake{Results: map[string]run.Result{
		gitPathKey(dir): {Stdout: ".git/rebase-merge\n.git/rebase-apply\n.git/MERGE_HEAD\n" +
			".git/CHERRY_PICK_HEAD\n.git/BISECT_LOG\n"},
		"git -C " + dir + " symbolic-ref --short -q HEAD": {Stdout: "main\n"},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: dir}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !containsCall(fake.Calls, "git -C "+dir+" fetch origin --prune") {
		t.Errorf("Calls = %v, want the fetch to run", joinedCalls(fake.Calls))
	}
	if _, ok := findEvent(events, StatusDone); !ok {
		t.Errorf("events = %+v, want a Done event", events)
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
	fake := &seqFake{
		Fake: &run.Fake{Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":         {},
			"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
			"git -C /r/app status --porcelain":           {Stdout: "M file\n"},
			stashPushKey:                                 {},
			"git -C /r/app stash list --format=%H":       {Stdout: "ours\n"},
			"git -C /r/app stash pop --quiet stash@{0}":  {},
		}},
		seq: map[string][]step{stashTipKey: {noStash, {res: run.Result{Stdout: "ours\n"}}}},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, stashPushKey) {
		t.Errorf("Calls = %v, want a stash push call", joinedCalls(fake.Calls))
	}
	if !containsCall(fake.Calls, "git -C /r/app stash pop --quiet stash@{0}") {
		t.Errorf("Calls = %v, want a stash pop of stash@{0}", joinedCalls(fake.Calls))
	}
	if _, ok := findEvent(events, StatusStashConflict); ok {
		t.Errorf("events contains StashConflict, want none: %+v", events)
	}
}

// TestSyncStashPushSavedNothing covers a worktree that read dirty but
// whose stash push saved nothing (e.g. the change was reverted in
// between): refs/stash is unchanged, so the pre-existing top stash
// belongs to someone else and must not be popped.
func TestSyncStashPushSavedNothing(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app fetch origin --prune":         {},
		"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
		"git -C /r/app status --porcelain":           {Stdout: "M file\n"},
		stashPushKey:                                 {},
		stashTipKey:                                  {Stdout: "theirs\n"},
	}}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, stashPushKey) {
		t.Errorf("Calls = %v, want a stash push call", joinedCalls(fake.Calls))
	}
	if containsSubstring(fake.Calls, "stash pop") || containsSubstring(fake.Calls, "stash list") {
		t.Errorf("Calls = %v, want no stash list or pop", joinedCalls(fake.Calls))
	}
	if _, ok := findEvent(events, StatusStashConflict); ok {
		t.Errorf("events contains StashConflict, want none: %+v", events)
	}
}

// TestSyncPopsOwnStashByIndex covers another session pushing a stash
// on top of ours mid-sync: the pop must target ours by index, not
// whatever is on top.
func TestSyncPopsOwnStashByIndex(t *testing.T) {
	fake := &seqFake{
		Fake: &run.Fake{Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":         {},
			"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
			"git -C /r/app status --porcelain":           {Stdout: "M file\n"},
			stashPushKey:                                 {},
			"git -C /r/app stash list --format=%H":       {Stdout: "theirs\nours\nolder\n"},
			"git -C /r/app stash pop --quiet stash@{1}":  {},
		}},
		seq: map[string][]step{stashTipKey: {{res: run.Result{Stdout: "older\n"}}, {res: run.Result{Stdout: "ours\n"}}}},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !containsCall(fake.Calls, "git -C /r/app stash pop --quiet stash@{1}") {
		t.Errorf("Calls = %v, want a stash pop of stash@{1}", joinedCalls(fake.Calls))
	}
	if containsCall(fake.Calls, "git -C /r/app stash pop --quiet stash@{0}") {
		t.Errorf("Calls = %v, want no pop of the other session's stash@{0}", joinedCalls(fake.Calls))
	}
	if _, ok := findEvent(events, StatusStashConflict); ok {
		t.Errorf("events contains StashConflict, want none: %+v", events)
	}
}

func TestSyncAutostashMissing(t *testing.T) {
	fake := &seqFake{
		Fake: &run.Fake{Results: map[string]run.Result{
			"git -C /r/app fetch origin --prune":         {},
			"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
			"git -C /r/app status --porcelain":           {Stdout: "M file\n"},
			stashPushKey:                                 {},
			"git -C /r/app stash list --format=%H":       {Stdout: "theirs\n"},
		}},
		seq: map[string][]step{stashTipKey: {noStash, {res: run.Result{Stdout: "ours\n"}}}},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	ev, ok := findEvent(events, StatusStashConflict)
	want := "autostash ours not found; check git stash list"
	if !ok || ev.Detail != want {
		t.Fatalf("events = %+v, want a StashConflict event with Detail %q", events, want)
	}
	if containsSubstring(fake.Calls, "stash pop") {
		t.Errorf("Calls = %v, want no stash pop", joinedCalls(fake.Calls))
	}
}

func TestSyncStashPopConflict(t *testing.T) {
	fake := &seqFake{
		Fake: &run.Fake{
			Results: map[string]run.Result{
				"git -C /r/app fetch origin --prune":         {},
				"git -C /r/app symbolic-ref --short -q HEAD": {Stdout: "main\n"},
				"git -C /r/app status --porcelain":           {Stdout: "M file\n"},
				stashPushKey:                                 {},
				"git -C /r/app stash list --format=%H":       {Stdout: "ours\n"},
			},
			Errs: map[string]error{
				"git -C /r/app stash pop --quiet stash@{0}": errors.New("exit status 1"),
			},
		},
		seq: map[string][]step{stashTipKey: {noStash, {res: run.Result{Stdout: "ours\n"}}}},
	}
	e := Engine{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	var events []Event
	if err := e.Run(nil, collect(&events)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	ev, ok := findEvent(events, StatusStashConflict)
	want := "stash pop conflicted; resolve manually (git stash list)"
	if !ok || ev.Detail != want {
		t.Fatalf("events = %+v, want a StashConflict event with Detail %q", events, want)
	}
}

// TestSyncKeepsStashWhenRestoreFails covers the starting HEAD failing
// to check back out: popping then would apply the stash onto the
// wrong branch, so it must be kept.
func TestSyncKeepsStashWhenRestoreFails(t *testing.T) {
	fake := &seqFake{
		Fake: &run.Fake{
			Results: map[string]run.Result{
				"git -C /r/app fetch origin --prune":                     {},
				"git -C /r/app status --porcelain":                       {Stdout: "M file\n"},
				stashPushKey:                                             {},
				"git -C /r/app rev-parse feature":                        {Stdout: "aaa\n"},
				"git -C /r/app rev-parse origin/feature":                 {Stdout: "bbb\n"},
				"git -C /r/app rev-list --count feature..origin/feature": {Stdout: "1\n"},
				"git -C /r/app checkout --quiet feature":                 {},
				"git -C /r/app rebase origin/feature --quiet":            {},
			},
			Errs: map[string]error{
				"git -C /r/app merge-base --is-ancestor feature origin/feature": errors.New("exit status 1"),
				"git -C /r/app checkout --quiet main":                           errors.New("exit status 1"),
			},
		},
		seq: map[string][]step{
			stashTipKey: {noStash, {res: run.Result{Stdout: "ours\n"}}},
			"git -C /r/app symbolic-ref --short -q HEAD": {
				{res: run.Result{Stdout: "main\n"}},
				{res: run.Result{Stdout: "feature\n"}},
			},
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

	ev, ok := findEvent(events, StatusStashConflict)
	want := "stash kept; HEAD not restored (on feature)"
	if !ok || ev.Detail != want {
		t.Fatalf("events = %+v, want a StashConflict event with Detail %q", events, want)
	}
	if containsSubstring(fake.Calls, "stash pop") {
		t.Errorf("Calls = %v, want no stash pop", joinedCalls(fake.Calls))
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
