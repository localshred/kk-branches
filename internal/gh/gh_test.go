package gh

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/localshred/koolkit/pkg/run"
)

// stubRunner is a recording Runner that returns one scripted Result/error
// per call, in call order. run.Fake cannot be used here because the
// GraphQL query string is far too long to use as a map key.
type stubRunner struct {
	results []run.Result
	errs    []error
	calls   [][]string
}

func (s *stubRunner) Run(name string, args ...string) (run.Result, error) {
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

func (s *stubRunner) Look(name string) (string, bool) { return "", false }

func nodeJSON(state string, number int, checksState string) string {
	rollup := "null"
	if checksState != "" {
		rollup = fmt.Sprintf(`{"state":%q}`, checksState)
	}
	return fmt.Sprintf(`{"number":%d,"url":"https://example.com/%d","state":%q,
		"title":"t%d","headRefOid":"oid%d","updatedAt":"2024-01-0%dT00:00:00Z",
		"commits":{"nodes":[{"commit":{"statusCheckRollup":%s}}]}}`,
		number, number, state, number, number, number, rollup)
}

func TestRepoParsesOwnerName(t *testing.T) {
	stub := &stubRunner{results: []run.Result{
		{Stdout: `{"owner":{"login":"o"},"name":"r"}`},
	}}
	c := Client{Run: stub}

	owner, name, err := c.Repo()
	if err != nil {
		t.Fatalf("Repo() error = %v", err)
	}
	if owner != "o" || name != "r" {
		t.Errorf("Repo() = (%q, %q), want (%q, %q)", owner, name, "o", "r")
	}
}

func TestPRsByHeadPrefersOpen(t *testing.T) {
	body := fmt.Sprintf(`{"data":{"repository":{"p0":{"nodes":[%s,%s]}}}}`,
		nodeJSON("MERGED", 1, ""), nodeJSON("OPEN", 2, "SUCCESS"))
	stub := &stubRunner{results: []run.Result{{Stdout: body}}}
	c := Client{Run: stub}

	prs, err := c.PRsByHead("o", "r", []string{"feature/x"})
	if err != nil {
		t.Fatalf("PRsByHead() error = %v", err)
	}
	pr, ok := prs["feature/x"]
	if !ok {
		t.Fatal("PRsByHead() missing entry for feature/x")
	}
	if pr.State != "OPEN" || pr.Number != 2 {
		t.Errorf("PRsByHead() picked %+v, want the OPEN node", pr)
	}
}

func TestPRsByHeadMostRecentWhenNoneOpen(t *testing.T) {
	body := fmt.Sprintf(`{"data":{"repository":{"p0":{"nodes":[%s,%s]}}}}`,
		nodeJSON("MERGED", 1, ""), nodeJSON("CLOSED", 2, ""))
	stub := &stubRunner{results: []run.Result{{Stdout: body}}}
	c := Client{Run: stub}

	prs, err := c.PRsByHead("o", "r", []string{"feature/x"})
	if err != nil {
		t.Fatalf("PRsByHead() error = %v", err)
	}
	pr, ok := prs["feature/x"]
	if !ok {
		t.Fatal("PRsByHead() missing entry for feature/x")
	}
	if pr.State != "MERGED" || pr.Number != 1 {
		t.Errorf("PRsByHead() picked %+v, want the first (most recent) node", pr)
	}
}

func TestPRsByHeadUsesVariables(t *testing.T) {
	evil := `x"){evil}`
	stub := &stubRunner{results: []run.Result{
		{Stdout: `{"data":{"repository":{"p0":{"nodes":[]}}}}`},
	}}
	c := Client{Run: stub}

	if _, err := c.PRsByHead("o", "r", []string{evil}); err != nil {
		t.Fatalf("PRsByHead() error = %v", err)
	}

	if len(stub.calls) != 1 {
		t.Fatalf("len(stub.calls) = %d, want 1", len(stub.calls))
	}
	args := stub.calls[0]

	var query, h0 string
	for i, arg := range args {
		if arg == "-f" && i+1 < len(args) {
			if strings.HasPrefix(args[i+1], "query=") {
				query = strings.TrimPrefix(args[i+1], "query=")
			}
			if args[i+1] == "h0="+evil {
				h0 = args[i+1]
			}
		}
	}
	if query == "" {
		t.Fatal("no -f query=... argument found")
	}
	if strings.Contains(query, evil) {
		t.Errorf("query contains the raw head value %q, want it only in a variable", evil)
	}
	if h0 == "" {
		t.Errorf("no -f h0=%s argument found", evil)
	}
}

func TestPRsByHeadChunks(t *testing.T) {
	heads := make([]string, 85)
	for i := range heads {
		heads[i] = fmt.Sprintf("b%d", i)
	}
	empty := `{"data":{"repository":{}}}`
	stub := &stubRunner{results: []run.Result{
		{Stdout: empty}, {Stdout: empty}, {Stdout: empty},
	}}
	c := Client{Run: stub}

	if _, err := c.PRsByHead("o", "r", heads); err != nil {
		t.Fatalf("PRsByHead() error = %v", err)
	}
	if len(stub.calls) != 3 {
		t.Fatalf("len(stub.calls) = %d, want 3", len(stub.calls))
	}

	countHeadArgs := func(args []string) int {
		n := 0
		for i, arg := range args {
			if arg == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "h") {
				n++
			}
		}
		return n
	}
	want := []int{40, 40, 5}
	for i, w := range want {
		if got := countHeadArgs(stub.calls[i]); got != w {
			t.Errorf("call %d has %d head args, want %d", i, got, w)
		}
	}
}

func TestPRsByHeadErrorPropagates(t *testing.T) {
	stub := &stubRunner{
		results: []run.Result{{Stderr: "fatal: bad credentials"}},
		errs:    []error{errors.New("exit status 1")},
	}
	c := Client{Run: stub}

	_, err := c.PRsByHead("o", "r", []string{"feature/x"})
	if err == nil {
		t.Fatal("PRsByHead() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "fatal: bad credentials") {
		t.Errorf("PRsByHead() error = %q, want it to contain trimmed stderr", err.Error())
	}
}

func TestChecksMapping(t *testing.T) {
	rollup := func(state string) commitConnection {
		return commitConnection{Nodes: []commitNode{
			{Commit: commitDetail{StatusCheckRollup: &statusCheckRollup{State: state}}},
		}}
	}

	tests := []struct {
		name    string
		commits commitConnection
		want    string
	}{
		{"success", rollup("SUCCESS"), "passing"},
		{"failure", rollup("FAILURE"), "failing"},
		{"error", rollup("ERROR"), "failing"},
		{"pending", rollup("PENDING"), "running"},
		{"expected", rollup("EXPECTED"), "running"},
		{"unknown", rollup("WEIRD"), ""},
		{"nil rollup", commitConnection{Nodes: []commitNode{{Commit: commitDetail{}}}}, ""},
		{"no commits", commitConnection{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checksState(tt.commits); got != tt.want {
				t.Errorf("checksState() = %q, want %q", got, tt.want)
			}
		})
	}
}
