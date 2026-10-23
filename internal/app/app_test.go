package app

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/localshred/kk-branches/internal/gh"
	"github.com/localshred/kk-branches/internal/git"
	"github.com/localshred/kk-branches/internal/model"
	"github.com/localshred/koolkit/pkg/run"
)

// ghStub is a recording Runner that returns one scripted Result/error
// per call, in call order. run.Fake cannot be used for gh calls
// because the GraphQL query string is far too long to use as a map
// key (see internal/gh/gh_test.go).
type ghStub struct {
	results []run.Result
	errs    []error
	calls   [][]string
}

func (s *ghStub) Run(name string, args ...string) (run.Result, error) {
	i := len(s.calls)
	s.calls = append(s.calls, append([]string{name}, args...))
	var res run.Result
	if i < len(s.results) {
		res = s.results[i]
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return res, err
}

func (s *ghStub) Look(name string) (string, bool) { return "", false }

// joinedCalls returns the space-joined form of every call recorded in
// calls, matching run.Fake's own key format.
func joinedCalls(calls [][]string) []string {
	out := make([]string, len(calls))
	for i, call := range calls {
		out[i] = strings.Join(call, " ")
	}
	return out
}

func TestCreateUsesNoTrack(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app check-ref-format --branch alice/x":     {},
		"git -C /r/app branch --no-track alice/x origin/main": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	if _, err := a.Create(model.Snapshot{}, CreateRequest{
		Name: "alice/x", Base: "origin/main", Then: ThenCreate,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	calls := joinedCalls(fake.Calls)
	want := "git -C /r/app branch --no-track alice/x origin/main"
	found := false
	for _, call := range calls {
		if call == want {
			found = true
		}
		if strings.Contains(call, "--track") || strings.Contains(call, "checkout -b") {
			t.Errorf("Calls contains %q, want no --track or checkout -b", call)
		}
	}
	if !found {
		t.Errorf("Calls = %v, want it to contain %q", calls, want)
	}
}

func TestCreateRejectsExisting(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app check-ref-format --branch alice/x": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	s := model.Snapshot{Branches: []model.Branch{{Name: "alice/x"}}}

	_, err := a.Create(s, CreateRequest{Name: "alice/x", Base: "origin/main", Then: ThenCreate})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create() error = %v, want it to contain %q", err, "already exists")
	}
	for _, call := range joinedCalls(fake.Calls) {
		if strings.Contains(call, "branch --no-track") {
			t.Errorf("Calls contains %q, want no branch --no-track call", call)
		}
	}
}

func TestCreateWorktreeCollision(t *testing.T) {
	mainPath := t.TempDir()
	collidePath := model.WorktreePath(mainPath, "alice/x")
	if err := os.MkdirAll(collidePath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	fake := &run.Fake{Results: map[string]run.Result{
		"git -C " + mainPath + " check-ref-format --branch alice/x":     {},
		"git -C " + mainPath + " branch --no-track alice/x origin/main": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: mainPath}}
	s := model.Snapshot{MainPath: mainPath}

	_, err := a.Create(s, CreateRequest{Name: "alice/x", Base: "origin/main", Then: ThenWorktree})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create() error = %v, want it to contain %q", err, "already exists")
	}
	for _, call := range joinedCalls(fake.Calls) {
		if strings.Contains(call, "worktree add") {
			t.Errorf("Calls contains %q, want no worktree add call", call)
		}
	}
}

// TestCreateThenWorktreeDetachedMain exercises Create's ThenWorktree
// path when the main checkout's branch is not tracked on any
// model.Branch (e.g. it is in detached HEAD state). Snapshot.MainPath
// is set directly from Repo.Worktrees() by Load regardless of
// whether the main worktree is attached to a branch, so Create must
// use it rather than scanning s.Branches for a Worktree.Main entry.
func TestCreateThenWorktreeDetachedMain(t *testing.T) {
	mainPath := t.TempDir()
	wantPath := model.WorktreePath(mainPath, "alice/x")

	fake := &run.Fake{Results: map[string]run.Result{
		"git -C " + mainPath + " check-ref-format --branch alice/x":     {},
		"git -C " + mainPath + " branch --no-track alice/x origin/main": {},
		"git -C " + mainPath + " worktree add " + wantPath + " alice/x": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: mainPath}}
	s := model.Snapshot{MainPath: mainPath}

	path, err := a.Create(s, CreateRequest{Name: "alice/x", Base: "origin/main", Then: ThenWorktree})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if path != wantPath {
		t.Errorf("Create() path = %q, want %q", path, wantPath)
	}
}

// TestAddWorktreeDetachedMain is AddWorktree's counterpart to
// TestCreateThenWorktreeDetachedMain.
func TestAddWorktreeDetachedMain(t *testing.T) {
	mainPath := t.TempDir()
	wantPath := model.WorktreePath(mainPath, "bob/y")

	fake := &run.Fake{Results: map[string]run.Result{
		"git -C " + mainPath + " worktree add " + wantPath + " bob/y": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: mainPath}}
	s := model.Snapshot{MainPath: mainPath}

	path, err := a.AddWorktree(s, "bob/y")
	if err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}
	if path != wantPath {
		t.Errorf("AddWorktree() path = %q, want %q", path, wantPath)
	}
}

func TestDeleteRechecksDirty(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app-wt-alice-x status --porcelain": {Stdout: " M f.go\n"},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	b := model.Branch{
		Name:     "alice/x",
		Worktree: &model.Worktree{Path: "/r/app-wt-alice-x", Dirty: false},
		Lookup:   model.PRFound,
		PR:       &model.PR{Number: 1, State: "MERGED"},
		InPR:     true,
	}
	if got := model.Assess(b).Level; got != model.LevelSafe {
		t.Fatalf("test setup: Assess(b).Level = %v, want LevelSafe", got)
	}

	err := a.Delete(b)
	if err == nil || !strings.Contains(err.Error(), "worktree became dirty") {
		t.Fatalf("Delete() error = %v, want it to contain %q", err, "worktree became dirty")
	}
	for _, call := range joinedCalls(fake.Calls) {
		if strings.Contains(call, "worktree remove") || strings.Contains(call, "branch -D") {
			t.Errorf("Calls contains %q, want no worktree remove or branch -D call", call)
		}
	}
}

func TestDeleteRemovesWorktreeThenBranch(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app-wt-alice-x status --porcelain":     {},
		"git -C /r/app worktree remove /r/app-wt-alice-x": {},
		"git -C /r/app branch -D alice/x":                 {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	b := model.Branch{
		Name:     "alice/x",
		Worktree: &model.Worktree{Path: "/r/app-wt-alice-x"},
		Lookup:   model.PRFound,
		PR:       &model.PR{Number: 1, State: "MERGED"},
		InPR:     true,
	}

	if err := a.Delete(b); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	want := []string{
		"git -C /r/app-wt-alice-x status --porcelain",
		"git -C /r/app worktree remove /r/app-wt-alice-x",
		"git -C /r/app branch -D alice/x",
	}
	if got := joinedCalls(fake.Calls); !reflect.DeepEqual(got, want) {
		t.Errorf("Calls = %v, want %v", got, want)
	}
}

func TestDeleteRefusesBlocked(t *testing.T) {
	fake := &run.Fake{}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	b := model.Branch{
		Name:     "alice/x",
		Worktree: &model.Worktree{Path: "/r/app", Main: true},
	}

	if err := a.Delete(b); err == nil {
		t.Fatal("Delete() error = nil, want non-nil")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("Calls = %v, want no calls", fake.Calls)
	}
}

func TestRemoveWorktreeRefusesMain(t *testing.T) {
	fake := &run.Fake{}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}
	b := model.Branch{Name: "main", Worktree: &model.Worktree{Path: "/r/app", Main: true}}

	if err := a.RemoveWorktree(b); err == nil {
		t.Fatal("RemoveWorktree() error = nil, want non-nil")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("Calls = %v, want no calls", fake.Calls)
	}
}

func TestLoadJoinsWorktrees(t *testing.T) {
	refFormat := "git -C /r/app for-each-ref --format=%(refname:short)%00%(objectname)%00" +
		"%(authoremail)%00%(committerdate:unix)%00%(upstream:short)%00%(upstream:remoteref)%00" +
		"%(upstream:track,nobracket) refs/heads/"
	fake := &run.Fake{Results: map[string]run.Result{
		refFormat: {
			Stdout: "main\x00tip1\x00<a@example.com>\x001000\x00\x00\x00\n" +
				"alice/x\x00tip2\x00<a@example.com>\x001001\x00\x00\x00\n",
		},
		"git -C /r/app worktree list --porcelain": {
			Stdout: "worktree /r/app\nHEAD tip1\nbranch refs/heads/main\n\n" +
				"worktree /r/app-wt-alice-x\nHEAD tip2\nbranch refs/heads/alice/x\n\n",
		},
		"git -C /r/app symbolic-ref --short refs/remotes/origin/HEAD": {Stdout: "main\n"},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	snap, err := a.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var alice model.Branch
	found := false
	for _, b := range snap.Branches {
		if b.Name == "alice/x" {
			alice, found = b, true
		}
	}
	if !found {
		t.Fatal("Load() missing branch alice/x")
	}
	if alice.Worktree == nil || alice.Worktree.Path != "/r/app-wt-alice-x" {
		t.Errorf("alice/x Worktree = %+v, want path /r/app-wt-alice-x", alice.Worktree)
	}
	if alice.Trunk {
		t.Error("alice/x Trunk = true, want false")
	}
	if alice.Lookup != model.PRPending {
		t.Errorf("alice/x Lookup = %v, want PRPending", alice.Lookup)
	}
	if snap.Repo != "app" {
		t.Errorf("snap.Repo = %q, want %q", snap.Repo, "app")
	}
	if snap.Trunk != "main" {
		t.Errorf("snap.Trunk = %q, want %q", snap.Trunk, "main")
	}
}

func TestLoadPRsUnknownOnError(t *testing.T) {
	fake := &run.Fake{Errs: map[string]error{
		"gh repo view --json owner,name": errors.New("exit status 1"),
	}}
	a := App{GH: gh.Client{Run: fake}}
	s := model.Snapshot{Branches: []model.Branch{
		{Name: "main", Trunk: true},
		{Name: "alice/x"},
		{Name: "bob/y"},
	}}

	got, err := a.LoadPRs(s)
	if err == nil {
		t.Fatal("LoadPRs() error = nil, want non-nil")
	}
	for _, b := range got.Branches {
		if b.Trunk {
			continue
		}
		if b.Lookup != model.PRUnknown {
			t.Errorf("branch %s Lookup = %v, want PRUnknown", b.Name, b.Lookup)
		}
	}
}

func TestLoadPRsInPRByAncestor(t *testing.T) {
	body := `{"data":{"repository":{"p0":{"nodes":[{"number":1,"url":"https://example.com/1",` +
		`"state":"MERGED","title":"t","headRefOid":"headoid","updatedAt":"2024-01-01T00:00:00Z",` +
		`"commits":{"nodes":[]}}]}}}}`
	ghRunner := &ghStub{results: []run.Result{
		{Stdout: `{"owner":{"login":"o"},"name":"r"}`},
		{Stdout: body},
	}}
	gitFake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app merge-base --is-ancestor localtip headoid": {},
	}}
	a := App{
		Repo: git.Repo{Run: gitFake, Dir: "/r/app"},
		GH:   gh.Client{Run: ghRunner},
	}
	s := model.Snapshot{Branches: []model.Branch{
		{Name: "alice/x", Tip: "localtip"},
	}}

	got, err := a.LoadPRs(s)
	if err != nil {
		t.Fatalf("LoadPRs() error = %v", err)
	}
	b := got.Branches[0]
	if b.Lookup != model.PRFound {
		t.Fatalf("Lookup = %v, want PRFound", b.Lookup)
	}
	if !b.InPR {
		t.Error("InPR = false, want true via ancestor check")
	}

	wantCall := []string{"git", "-C", "/r/app", "merge-base", "--is-ancestor", "localtip", "headoid"}
	if len(gitFake.Calls) != 1 || !reflect.DeepEqual(gitFake.Calls[0], wantCall) {
		t.Fatalf("gitFake.Calls = %v, want exactly one call %v", gitFake.Calls, wantCall)
	}
}

// TestLoadPRsNotInPRWhenAncestorCheckFails guards against a swapped
// IsAncestor(a, b) argument order: if the ancestor check errors (as
// it does whenever the PR head object isn't present locally),
// IsPR must stay false rather than defaulting to true, which would
// mark a branch with unpushed commits Safe to delete.
func TestLoadPRsNotInPRWhenAncestorCheckFails(t *testing.T) {
	body := `{"data":{"repository":{"p0":{"nodes":[{"number":1,"url":"https://example.com/1",` +
		`"state":"MERGED","title":"t","headRefOid":"headoid","updatedAt":"2024-01-01T00:00:00Z",` +
		`"commits":{"nodes":[]}}]}}}}`
	ghRunner := &ghStub{results: []run.Result{
		{Stdout: `{"owner":{"login":"o"},"name":"r"}`},
		{Stdout: body},
	}}
	gitFake := &run.Fake{Errs: map[string]error{
		"git -C /r/app merge-base --is-ancestor localtip headoid": errors.New("exit status 1"),
	}}
	a := App{
		Repo: git.Repo{Run: gitFake, Dir: "/r/app"},
		GH:   gh.Client{Run: ghRunner},
	}
	s := model.Snapshot{Branches: []model.Branch{
		{Name: "alice/x", Tip: "localtip"},
	}}

	got, err := a.LoadPRs(s)
	if err != nil {
		t.Fatalf("LoadPRs() error = %v", err)
	}
	b := got.Branches[0]
	if b.Lookup != model.PRFound {
		t.Fatalf("Lookup = %v, want PRFound", b.Lookup)
	}
	if b.InPR {
		t.Error("InPR = true, want false when the ancestor check fails and tips differ")
	}
	if level := model.Assess(b).Level; level == model.LevelSafe {
		t.Errorf("Assess(b).Level = %v, want not Safe when InPR is false", level)
	}
}

// TestLoadPRsDoesNotMutateInput guards against LoadPRs writing
// through to the caller's Branches backing array, which would race
// with a concurrent read of the snapshot it was given (e.g. a TUI
// running LoadPRs in a goroutine while View reads the old snapshot).
func TestLoadPRsDoesNotMutateInput(t *testing.T) {
	fake := &run.Fake{Errs: map[string]error{
		"gh repo view --json owner,name": errors.New("exit status 1"),
	}}
	a := App{GH: gh.Client{Run: fake}}
	original := []model.Branch{{Name: "alice/x", Lookup: model.PRPending}}
	s := model.Snapshot{Branches: original}

	if _, err := a.LoadPRs(s); err == nil {
		t.Fatal("LoadPRs() error = nil, want non-nil")
	}

	if original[0].Lookup != model.PRPending {
		t.Errorf("input branch Lookup = %v, want unchanged PRPending", original[0].Lookup)
	}
}

func TestBasesDefaultThenReleases(t *testing.T) {
	fake := &run.Fake{Results: map[string]run.Result{
		"git -C /r/app for-each-ref --sort=-version:refname --count=5 --format=%(refname:short) refs/remotes/origin/release-*": {
			Stdout: "origin/release-9\norigin/release-8\n",
		},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	got := a.Bases(model.Snapshot{Trunk: "develop"})
	want := []string{"origin/develop", "origin/release-9", "origin/release-8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Bases() = %v, want %v", got, want)
	}

	got = a.Bases(model.Snapshot{})
	want = []string{"origin/main", "origin/release-9", "origin/release-8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Bases() = %v, want %v", got, want)
	}
}

func TestOpenUsesPlatformCommand(t *testing.T) {
	want := "xdg-open"
	if runtime.GOOS == "darwin" {
		want = "open"
	}
	fake := &run.Fake{Results: map[string]run.Result{
		want + " https://example.com": {},
	}}
	a := App{Repo: git.Repo{Run: fake, Dir: "/r/app"}}

	if err := a.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0][0] != want {
		t.Errorf("Calls = %v, want a single %s call", fake.Calls, want)
	}
}
