// Package bd is a thin, read-only wrapper over the `bd` (beads) CLI. It shells
// out and parses the JSON that bd emits, so beadwatch never touches the Dolt
// store directly.
//
// This is a deliberately narrow slice of strand's internal/bd (which is 1887
// lines and mostly write verbs): only the four reads the counts derivation
// runs — List, Deps, Stats, EpicStatus. No write verb (Update, Claim, Close,
// Create, DepAdd, LabelAdd, Comment, Delete, …) comes along (bw-4wu Rules: "No
// write verbs from strand's internal/bd come along").
package bd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound means bd has no issue with the requested ID.
var ErrNotFound = errors.New("issue not found")

// ErrInvalidArg means bd rejected an argument (e.g. an unknown --status value).
var ErrInvalidArg = errors.New("invalid argument")

// ErrBD wraps any non-zero bd exit or bd-reported error not otherwise classified.
var ErrBD = errors.New("bd command failed")

// classify maps a bd error message to a typed sentinel so a caller can choose
// the right response. bd gives us only message text — no codes — so we match
// the stable phrases bd emits ("no issue found", "invalid status …"). Anything
// unrecognized is a true bd failure (ErrBD).
func classify(msg string) error {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "no issue found"), strings.Contains(low, "no issues found"):
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case strings.HasPrefix(low, "invalid "):
		return fmt.Errorf("%w: %s", ErrInvalidArg, msg)
	default:
		return fmt.Errorf("%w: %s", ErrBD, msg)
	}
}

// execMu serializes every bd invocation process-wide. beads' embedded Dolt store
// is a single-writer lock — concurrent bd calls collide and can corrupt or
// error. One global lock over-serializes across distinct repos, but beadwatch
// is a single localhost process and that cost is nil next to a corrupted store.
//
// It is a capacity-1 channel, not a sync.Mutex, so acquisition can honor the
// caller's context: a send takes the lock, a receive releases it, and a select
// on ctx.Done() lets a caller whose deadline passes while it waits bail out
// with ctx.Err() instead of blocking forever behind a hung holder.
var execMu = make(chan struct{}, 1)

// Issue mirrors the JSON shape bd emits from `list`. Fields bd omits stay at
// their zero value; extra fields bd adds later are ignored, not an error.
type Issue struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	// Priority is 0–4 (0=highest) when bd emits the field, nil when bd omits it.
	// *int (not plain int) so an absent priority decodes to nil rather than
	// collapsing into a false P0.
	Priority  *int   `json:"priority"`
	IssueType string `json:"issue_type"`
	// AwaitType is set only on a gate issue (IssueType=="gate"): what it waits
	// on — "human", "timer", "gh:run", "gh:pr", or "bead" (bd gate create
	// --await <type>). pulse reads it to recognize a formal human gate
	// alongside the legacy "human" label (bw-avk).
	AwaitType       string    `json:"await_type,omitempty"`
	Parent          string    `json:"parent,omitempty"`
	Description     string    `json:"description,omitempty"`
	Design          string    `json:"design,omitempty"`
	Assignee        string    `json:"assignee,omitempty"`
	Labels          []string  `json:"labels,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	DependencyCount int       `json:"dependency_count"`
	DependentCount  int       `json:"dependent_count"`
	CommentCount    int       `json:"comment_count"`
	// Metadata holds bd's custom key/values — the human-gate's review_needed
	// flag and the manual-rank order live here. bd emits numbers as JSON
	// numbers, so a rank decodes into any as float64.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Rank returns the bead's manual-rank order from metadata and whether it is
// set. bd stores rank as a JSON number (decoded to float64); a string is
// tolerated defensively in case a future bd quotes it. Absent or unparseable
// means unranked.
func (i *Issue) Rank() (float64, bool) {
	v, ok := i.Metadata["rank"]
	if !ok {
		return 0, false
	}
	switch r := v.(type) {
	case float64:
		return r, true
	case string:
		f, err := strconv.ParseFloat(r, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// DepEdge is one dependency edge: IssueID depends on DependsOnID. Type tells
// the real blocking dependency ("blocks") from epic hierarchy ("parent-child")
// and soft links ("relates_to"); the lane partition keeps only "blocks".
type DepEdge struct {
	IssueID     string  `json:"issue_id"`
	DependsOnID string  `json:"depends_on_id"`
	Type        DepType `json:"type"`
}

// Client runs bd commands against a single workspace directory.
type Client struct {
	// Dir is the working directory bd runs in (resolves the .beads workspace).
	// Empty means the process's current directory.
	Dir string
	// Bin is the bd binary name or path. Empty defaults to "bd" on PATH.
	Bin string
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "bd"
}

// run executes bd with args and returns stdout. A non-zero exit becomes an
// error carrying bd's stderr, which is usually a readable hint.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	// Acquire the single-writer lock, but honor ctx: if the caller's deadline
	// passes while it waits behind a held lock, return ctx.Err() rather than
	// block indefinitely. Fast-fail an already-done ctx: a free lock could
	// otherwise win the select pseudo-randomly over ctx.Done(), spawning a
	// doomed exec.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("bd: acquire write lock: %w", err)
	}
	select {
	case execMu <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("bd: acquire write lock: %w", ctx.Err())
	}
	defer func() { <-execMu }()
	// We hold the lock, but ctx may have fired while we waited behind a held
	// holder — the select can pick the send when both cases are ready. Surface
	// ctx.Err() rather than run a doomed command that misclassifies as ErrBD.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("bd: acquire write lock: %w", err)
	}
	//nolint:gosec // G204: bd is an operator-configured binary and args run via exec (no shell), so values like a status filter can't inject commands.
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = c.Dir
	var out bytes.Buffer
	var errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("bd %s: %w", strings.Join(args, " "), classify(msg))
	}
	return out.Bytes(), nil
}

// ListOpts is the typed read filter for List. The zero value is the full
// unfiltered read — every issue, bd's default row cap lifted — which is
// beadwatch's common path. A non-empty Status narrows the read to that one
// status.
type ListOpts struct {
	Status Status // when non-empty, filter to this status; empty = no filter
}

// List returns the repo's issues. It is the one place that knows bd's
// list-flag vocabulary: it always lifts bd's default row cap (--limit 0, since
// the derivation folds the whole set) and adds --status only when
// opts.Status is set. Callers speak intent through ListOpts, never CLI flags.
func (c *Client) List(ctx context.Context, opts ListOpts) ([]Issue, error) {
	args := []string{"list", "--json", "--limit", "0"}
	if opts.Status != "" {
		args = append(args, "--status", string(opts.Status))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssues(out)
}

// Stats mirrors the summary block of `bd stats --json`: the per-status issue
// counts. Fields bd omits stay zero; extra summary fields bd reports are
// ignored.
type Stats struct {
	Open       int `json:"open_issues"`
	InProgress int `json:"in_progress_issues"`
	Blocked    int `json:"blocked_issues"`
	Closed     int `json:"closed_issues"`
	Deferred   int `json:"deferred_issues"`
	Total      int `json:"total_issues"`
}

// Stats returns the repo's per-status issue counts from `bd stats --json`. It
// is the one read that sees the closed and deferred totals: `bd list` omits
// closed, so the row's ✓/❄ cells source their numbers here.
func (c *Client) Stats(ctx context.Context) (Stats, error) {
	out, err := c.run(ctx, "stats", "--json")
	if err != nil {
		return Stats{}, err
	}
	var resp struct {
		Summary Stats `json:"summary"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &resp); err != nil {
		return Stats{}, fmt.Errorf("parse bd stats: %w", err)
	}
	return resp.Summary, nil
}

// EpicStatus mirrors one row of `bd epic status --json`: an epic and its child
// roll-up. Fields bd omits stay zero.
type EpicStatus struct {
	Epic           EpicRef `json:"epic"`
	TotalChildren  int     `json:"total_children"`
	ClosedChildren int     `json:"closed_children"`
}

// EpicRef is the epic identity inside an EpicStatus row. Title is bd's own
// epic title, carried so a consumer naming an epic doesn't re-read the DAG
// for it.
type EpicRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
}

// EpicStatus returns every epic's child roll-up from `bd epic status --json`.
// An empty repo (bd emits `[]`) yields nil, not an error.
func (c *Client) EpicStatus(ctx context.Context) ([]EpicStatus, error) {
	out, err := c.run(ctx, "epic", "status", "--json")
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 || string(trimmed) == "[]" {
		return nil, nil
	}
	var rows []EpicStatus
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, fmt.Errorf("parse bd epic status: %w", err)
	}
	return rows, nil
}

// Deps returns the dependency edges touching the given issue IDs (direction
// "down": what each issue depends on). With no IDs it returns nil — bd needs
// at least one. Callers pass the full ID set to fetch the whole graph and
// filter by Type themselves.
//
// bd's `dep list --json` has two output shapes: with several IDs it emits
// flat edge records ({issue_id, depends_on_id, type}); with exactly one ID it
// emits the dependency *issues* instead. decodeEdges handles both so a
// one-issue query still yields edges.
func (c *Client) Deps(ctx context.Context, ids ...string) ([]DepEdge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := c.run(ctx, append([]string{"dep", "list", "--json"}, ids...)...)
	if err != nil {
		return nil, err
	}
	return decodeEdges(out, ids)
}

// decodeEdges parses `dep list --json` in either shape. The flat batch shape
// has issue_id/depends_on_id; the single-ID shape returns dependency issues,
// which we turn into edges from the one queried ID. An empty array (no deps)
// yields nil.
func decodeEdges(out []byte, ids []string) ([]DepEdge, error) {
	trimmed, err := trimForDecode(out)
	if trimmed == nil || err != nil {
		return nil, err
	}
	var rows []struct {
		IssueID        string `json:"issue_id"`
		DependsOnID    string `json:"depends_on_id"`
		Type           string `json:"type"`
		ID             string `json:"id"`              // single-ID shape: the dependency issue
		DependencyType string `json:"dependency_type"` // single-ID shape
	}
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, fmt.Errorf("parse bd dep list: %w", err)
	}
	edges := make([]DepEdge, 0, len(rows))
	for _, r := range rows {
		switch {
		case r.IssueID != "" && r.DependsOnID != "":
			edges = append(edges, DepEdge{IssueID: r.IssueID, DependsOnID: r.DependsOnID, Type: DepType(r.Type)})
		case r.ID != "" && len(ids) == 1:
			// Single-ID query: the row is a dependency of the one queried issue.
			edges = append(edges, DepEdge{IssueID: ids[0], DependsOnID: r.ID, Type: DepType(r.DependencyType)})
		}
	}
	return edges, nil
}

// decodeIssues parses bd's JSON. List-style commands emit an array. bd reports
// its own errors as a JSON object with an "error" key; surface those.
func decodeIssues(out []byte) ([]Issue, error) {
	trimmed, err := trimForDecode(out)
	if trimmed == nil || err != nil {
		return nil, err
	}
	if trimmed[0] == '{' {
		// trimForDecode already ruled out the error object; a non-error object
		// is a single issue, so wrap it.
		var issue Issue
		if err := json.Unmarshal(trimmed, &issue); err != nil {
			return nil, fmt.Errorf("parse bd output: %w", err)
		}
		return []Issue{issue}, nil
	}
	var issues []Issue
	if err := json.Unmarshal(trimmed, &issues); err != nil {
		return nil, fmt.Errorf("parse bd output: %w", err)
	}
	return issues, nil
}

// trimForDecode trims bd's JSON output and short-circuits the two cases every
// decoder shares: an empty or `[]` response (returns nil, nil) and a bd error
// object {"error": ...} (returns the classified error). Otherwise it returns
// the trimmed bytes for the caller to unmarshal. A nil return with nil error
// means "nothing to decode".
func trimForDecode(out []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) {
		return nil, nil
	}
	if trimmed[0] == '{' {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(trimmed, &e) == nil && e.Error != "" {
			return nil, classify(e.Error)
		}
	}
	return trimmed, nil
}
