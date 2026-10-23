// Package gh adapts koolkit's run.Runner to drive the gh(1) CLI for
// kk-branches: resolving the current repository's owner and name, and
// batching pull-request lookups by head branch name through gh api
// graphql.
package gh

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/localshred/koolkit/pkg/run"

	"github.com/localshred/kk-branches/internal/model"
)

// chunkSize is the maximum number of head branch names looked up in a
// single gh api graphql call.
const chunkSize = 40

// Client is a gh-CLI JSON client driven through run.Runner.
type Client struct {
	Run run.Runner
}

// PRsByHead looks up the most relevant pull request for each name in
// heads, batching requests into gh api graphql calls of up to chunkSize
// heads each. Heads with no matching pull request are absent from the
// returned map.
func (c Client) PRsByHead(owner, name string, heads []string) (map[string]model.PR, error) {
	prs := make(map[string]model.PR)
	for start := 0; start < len(heads); start += chunkSize {
		end := start + chunkSize
		if end > len(heads) {
			end = len(heads)
		}
		if err := c.prsByHeadChunk(owner, name, heads[start:end], prs); err != nil {
			return nil, err
		}
	}
	return prs, nil
}

// Repo returns the owner and name of the repository that gh is
// configured to operate against.
func (c Client) Repo() (owner, name string, err error) {
	res, runErr := c.Run.Run("gh", "repo", "view", "--json", "owner,name")
	if runErr != nil {
		return "", "", wrapGHError(runErr, res.Stderr)
	}
	var parsed struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return "", "", err
	}
	return parsed.Owner.Login, parsed.Name, nil
}

// buildQuery returns the GraphQL document that looks up n head branch
// names in a single repository query, one $h<i> variable and p<i> alias
// per head. Head values themselves are never interpolated into the
// query; they travel as -f h<i>=<value> variables.
func buildQuery(n int) string {
	var vars, fields strings.Builder
	fmt.Fprint(&vars, "$owner: String!, $name: String!")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&vars, ", $h%d: String!", i)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&fields, `    p%d: pullRequests(headRefName: $h%d, first: 5, states: [OPEN, MERGED, CLOSED], orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes { number url state title headRefOid updatedAt
              commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } }
    }
`, i, i)
	}
	return fmt.Sprintf("query(%s) {\n  repository(owner: $owner, name: $name) {\n%s  }\n}",
		vars.String(), fields.String())
}

// checksState maps a pull request's last commit's statusCheckRollup
// state to the model.PR.Checks vocabulary: SUCCESS -> "passing",
// FAILURE/ERROR -> "failing", PENDING/EXPECTED -> "running", anything
// else (including no rollup) -> "".
func checksState(commits commitConnection) string {
	if len(commits.Nodes) == 0 {
		return ""
	}
	rollup := commits.Nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return ""
	}
	switch rollup.State {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR":
		return "failing"
	case "PENDING", "EXPECTED":
		return "running"
	default:
		return ""
	}
}

// commitConnection is the JSON shape of a pull request's last-commit
// connection, used to read its status check rollup.
type commitConnection struct {
	Nodes []commitNode `json:"nodes"`
}

// commitDetail is the JSON shape of a single commit's status check
// rollup.
type commitDetail struct {
	StatusCheckRollup *statusCheckRollup `json:"statusCheckRollup"`
}

// commitNode is the JSON shape of one node in a commitConnection.
type commitNode struct {
	Commit commitDetail `json:"commit"`
}

// graphQLError is the JSON shape of one entry in a GraphQL response's
// top-level errors array.
type graphQLError struct {
	Message string `json:"message"`
}

// graphQLResponse is the JSON shape of a gh api graphql response: the
// aliased pullRequestConnection per head, keyed by its p<i> alias, plus
// any top-level errors.
type graphQLResponse struct {
	Data struct {
		Repository map[string]pullRequestConnection `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// pickNode returns the first OPEN node in nodes, else the first node.
// nodes is already ordered most-recently-updated first by the query, so
// the first node is also the most recent.
func pickNode(nodes []pullRequestNode) pullRequestNode {
	for _, n := range nodes {
		if n.State == "OPEN" {
			return n
		}
	}
	return nodes[0]
}

// prsByHeadChunk runs one gh api graphql call for heads (at most
// chunkSize) and writes the resolved model.PR for each matched head into
// result.
func (c Client) prsByHeadChunk(owner, name string, heads []string, result map[string]model.PR) error {
	args := []string{
		"api", "graphql",
		"-f", "query=" + buildQuery(len(heads)),
		"-f", "owner=" + owner,
		"-f", "name=" + name,
	}
	for i, head := range heads {
		args = append(args, "-f", fmt.Sprintf("h%d=%s", i, head))
	}

	res, err := c.Run.Run("gh", args...)
	if err != nil {
		return wrapGHError(err, res.Stderr)
	}

	var parsed graphQLResponse
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return err
	}
	if len(parsed.Errors) > 0 {
		return fmt.Errorf("gh api graphql: %s", parsed.Errors[0].Message)
	}

	for i, head := range heads {
		conn, ok := parsed.Data.Repository[fmt.Sprintf("p%d", i)]
		if !ok || len(conn.Nodes) == 0 {
			continue
		}
		result[head] = toPR(pickNode(conn.Nodes))
	}
	return nil
}

// pullRequestConnection is the JSON shape of one aliased pullRequests
// field in a graphQLResponse.
type pullRequestConnection struct {
	Nodes []pullRequestNode `json:"nodes"`
}

// pullRequestNode is the JSON shape of one pull request node returned by
// the GraphQL query.
type pullRequestNode struct {
	Number     int              `json:"number"`
	URL        string           `json:"url"`
	State      string           `json:"state"`
	Title      string           `json:"title"`
	HeadRefOID string           `json:"headRefOid"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	Commits    commitConnection `json:"commits"`
}

// statusCheckRollup is the JSON shape of a commit's status check rollup.
type statusCheckRollup struct {
	State string `json:"state"`
}

// toPR converts a pullRequestNode into a model.PR.
func toPR(n pullRequestNode) model.PR {
	return model.PR{
		Number:    n.Number,
		URL:       n.URL,
		State:     n.State,
		Title:     n.Title,
		HeadOID:   n.HeadRefOID,
		Checks:    checksState(n.Commits),
		UpdatedAt: n.UpdatedAt,
	}
}

// wrapGHError wraps err with stderr, trimmed, when stderr is non-empty.
func wrapGHError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
}
