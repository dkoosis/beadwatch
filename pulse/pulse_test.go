package pulse

import "testing"

// TestLaneOfDerivationTable pins the six rows of the design's §The derivation
// table (~/Projects/kg/Project/beadwatch/specs/beadwatch-design.md), "after"
// column — decision 477486825755: gated beats blocked beats open/in-progress,
// whatever the status or dependencies.
//
// The last two rows are not in that table: they pin the not-live guard that
// runs BEFORE the gate check. "Human wins every overlap" is bounded by being
// live — a closed or deferred bead is LaneNone even when it carries the human
// label and an unmet blocker, so a gated bead that gets closed leaves the
// waiting lane instead of parking there forever. Without these rows the
// short-circuit is only reached incidentally, through TestPartitionSumsToLiveBeads.
func TestLaneOfDerivationTable(t *testing.T) {
	cases := []struct {
		name              string
		status            Status
		gated, hasBlocker bool
		want              Lane
	}{
		{"open, human label, open dependency -> bh", StatusOpen, true, true, LaneWaiting},
		{"in_progress, human label -> bh only", StatusInProgress, true, false, LaneWaiting},
		{"status blocked, human label -> bh", StatusBlocked, true, false, LaneWaiting},
		{"open, open dependency -> bb", StatusOpen, false, true, LaneBlocked},
		{"in_progress, no label -> bw", StatusInProgress, false, false, LaneInProgress},
		{"open, plain -> bo", StatusOpen, false, false, LaneOpen},
		{"closed, human label, open dependency -> not live", StatusClosed, true, true, LaneNone},
		{"deferred, human label -> not live", StatusDeferred, true, false, LaneNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LaneOf(c.status, c.gated, c.hasBlocker); got != c.want {
				t.Errorf("LaneOf(%v, gated=%v, hasBlocker=%v) = %v, want %v", c.status, c.gated, c.hasBlocker, got, c.want)
			}
		})
	}
}

// TestPartitionSumsToLiveBeads is the partition property decision 477486825755
// promises: bh+bo+bw+bb equals the count of beads whose status is open,
// in_progress or blocked — nothing live is dropped and nothing is double
// counted. The fixture carries every gate/status/blocker overlap. Against the
// pre-change LaneOf this is red: an ungated in_progress bead (plain-inprogress)
// used to map to LaneNone (a raw ◐ count kept outside the partition), so it
// vanished from the sum instead of landing in bw.
func TestPartitionSumsToLiveBeads(t *testing.T) {
	issues := []Issue{
		{ID: "plain-open", Status: StatusOpen},
		{ID: "blocker", Status: StatusOpen},
		{ID: "open-blocked", Status: StatusOpen},
		{ID: "gated-open-blocked", Status: StatusOpen, Labels: []string{"human"}},
		{ID: "review-open", Status: StatusOpen, Metadata: map[string]any{"review_needed": "true"}},
		{ID: "plain-inprogress", Status: StatusInProgress},
		{ID: "gated-inprogress", Status: StatusInProgress, Labels: []string{"human"}},
		{ID: "plain-blocked-status", Status: StatusBlocked},
		{ID: "gated-blocked-status", Status: StatusBlocked, Labels: []string{"human"}},
		{ID: "closed", Status: StatusClosed},
		{ID: "deferred", Status: StatusDeferred},
	}
	deps := []DepEdge{
		{IssueID: "open-blocked", DependsOnID: "blocker", Type: DepBlocks},
		{IssueID: "gated-open-blocked", DependsOnID: "blocker", Type: DepBlocks},
	}

	lanes := Lanes(issues, deps)
	var bh, bo, bw, bb int
	for _, l := range lanes {
		switch l {
		case LaneWaiting:
			bh++
		case LaneOpen:
			bo++
		case LaneInProgress:
			bw++
		case LaneBlocked:
			bb++
		case LaneNone:
		}
	}

	var wantTotal int
	for i := range issues {
		switch issues[i].Status {
		case StatusOpen, StatusInProgress, StatusBlocked:
			wantTotal++
		case StatusClosed, StatusDeferred:
			// not live work — excluded from wantTotal
		}
	}

	if got := bh + bo + bw + bb; got != wantTotal {
		t.Errorf("bh+bo+bw+bb = %d (bh=%d bo=%d bw=%d bb=%d), want %d — the count of open/in_progress/blocked beads",
			got, bh, bo, bw, bb, wantTotal)
	}
}

func TestLanes(t *testing.T) {
	issues := []Issue{
		{ID: "open", Status: StatusOpen},
		{ID: "gated", Status: StatusOpen, Labels: []string{"human"}},
		{ID: "depblocked", Status: StatusOpen},
		{ID: "depblockedgated", Status: StatusOpen, Labels: []string{"human"}}, // ◆ — gated wins over the blocker
		{ID: "storedblocked", Status: StatusBlocked},
		{ID: "storedblockedgated", Status: StatusBlocked, Labels: []string{"human"}}, // ◆ — gated wins over the stored status
		{ID: "blocker", Status: StatusOpen},
		{ID: "activegated", Status: StatusInProgress, Labels: []string{"human"}}, // ◆
		{ID: "active", Status: StatusInProgress},                                 // ◐ — LaneInProgress, not omitted
		{ID: "closed", Status: StatusClosed},
		{ID: "deferred", Status: StatusDeferred},
	}
	deps := []DepEdge{
		{IssueID: "depblocked", DependsOnID: "blocker", Type: "blocks"},
		{IssueID: "depblockedgated", DependsOnID: "blocker", Type: "blocks"},
	}

	t.Run("warm deps", func(t *testing.T) {
		got := Lanes(issues, deps)
		want := map[string]Lane{
			"open":               LaneOpen,
			"blocker":            LaneOpen,
			"gated":              LaneWaiting,
			"activegated":        LaneWaiting,
			"active":             LaneInProgress,
			"depblocked":         LaneBlocked,
			"depblockedgated":    LaneWaiting, // gated beats blocker
			"storedblocked":      LaneBlocked,
			"storedblockedgated": LaneWaiting, // gated beats the stored status
			// closed, deferred → LaneNone, omitted from the map
		}
		assertLanes(t, got, want, issues)
		// disjointness is structural: each id maps to exactly one Lane, so no
		// separate cross-lane check is needed — a double-count is unrepresentable.
	})

	t.Run("cold deps (nil) — only dependency blocks resolve to open", func(t *testing.T) {
		got := Lanes(issues, nil)
		want := map[string]Lane{
			"open":               LaneOpen,
			"blocker":            LaneOpen,
			"gated":              LaneWaiting,
			"activegated":        LaneWaiting,
			"active":             LaneInProgress,
			"depblocked":         LaneOpen,    // no deps → not dep-blocked
			"depblockedgated":    LaneWaiting, // gated regardless of deps
			"storedblocked":      LaneBlocked, // stored status needs no deps
			"storedblockedgated": LaneWaiting, // gated still beats the stored status cold
		}
		assertLanes(t, got, want, issues)
	})
}

func assertLanes(t *testing.T, got, want map[string]Lane, issues []Issue) {
	t.Helper()
	for i := range issues {
		id := issues[i].ID
		if got[id] != want[id] {
			t.Errorf("Lanes[%q] = %d, want %d", id, got[id], want[id])
		}
	}
}

func TestHumanGate(t *testing.T) {
	cases := []struct {
		name                     string
		iss                      Issue
		wantDecision, wantReview bool
	}{
		{"plain", Issue{ID: "a"}, false, false},
		{"label human", Issue{Labels: []string{"core", "human"}}, true, false},
		{"review string true", Issue{Metadata: map[string]any{"review_needed": "true"}}, false, true},
		{"review bool true", Issue{Metadata: map[string]any{"review_needed": true}}, false, true},
		{"review false", Issue{Metadata: map[string]any{"review_needed": "false"}}, false, false},
		{"both decision wins", Issue{Labels: []string{"human"}, Metadata: map[string]any{"review_needed": "true"}}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, r := humanGate(&c.iss, false)
			if d != c.wantDecision || r != c.wantReview {
				t.Errorf("humanGate = (%v,%v), want (%v,%v)", d, r, c.wantDecision, c.wantReview)
			}
		})
	}
	t.Run("gated wins over a plain issue", func(t *testing.T) {
		d, r := humanGate(&Issue{ID: "a"}, true)
		if !d || r {
			t.Errorf("humanGate(plain, gated=true) = (%v,%v), want (true,false)", d, r)
		}
	})
}

// TestIsHumanGateIssue pins bw-avk's gate-recognition rule: a bead only reads
// as an unresolved human gate when it is a "gate" issue, awaiting "human",
// and still open. bd's other await types (timer, gh:run, gh:pr, bead) and a
// closed gate must not qualify.
func TestIsHumanGateIssue(t *testing.T) {
	cases := []struct {
		name string
		iss  *Issue
		want bool
	}{
		{"nil", nil, false},
		{"open human gate", &Issue{IssueType: "gate", AwaitType: "human", Status: StatusOpen}, true},
		{"closed gate blocks nothing", &Issue{IssueType: "gate", AwaitType: "human", Status: StatusClosed}, false},
		{"await timer is not human", &Issue{IssueType: "gate", AwaitType: "timer", Status: StatusOpen}, false},
		{"await gh:run is not human", &Issue{IssueType: "gate", AwaitType: "gh:run", Status: StatusOpen}, false},
		{"await gh:pr is not human", &Issue{IssueType: "gate", AwaitType: "gh:pr", Status: StatusOpen}, false},
		{"await bead is not human", &Issue{IssueType: "gate", AwaitType: "bead", Status: StatusOpen}, false},
		{"not a gate issue_type", &Issue{IssueType: "task", AwaitType: "human", Status: StatusOpen}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isHumanGateIssue(c.iss); got != c.want {
				t.Errorf("isHumanGateIssue(%+v) = %v, want %v", c.iss, got, c.want)
			}
		})
	}
}

// TestHumanGateBlocked pins bw-avk's blocker-scan: a bead is flagged only
// when a "blocks" edge points at an open human gate; a closed gate, a
// non-human await type, or a non-blocks edge type must not flag it.
func TestHumanGateBlocked(t *testing.T) {
	idx := map[string]Issue{
		"gate-open-human":   {ID: "gate-open-human", IssueType: "gate", AwaitType: "human", Status: StatusOpen},
		"gate-closed-human": {ID: "gate-closed-human", IssueType: "gate", AwaitType: "human", Status: StatusClosed},
		"gate-open-timer":   {ID: "gate-open-timer", IssueType: "gate", AwaitType: "timer", Status: StatusOpen},
		"ordinary":          {ID: "ordinary", Status: StatusOpen},
	}
	deps := []DepEdge{
		{IssueID: "bead-a", DependsOnID: "gate-open-human", Type: DepBlocks},
		{IssueID: "bead-b", DependsOnID: "gate-closed-human", Type: DepBlocks},
		{IssueID: "bead-c", DependsOnID: "gate-open-timer", Type: DepBlocks},
		{IssueID: "bead-d", DependsOnID: "ordinary", Type: DepBlocks},
		{IssueID: "bead-e", DependsOnID: "gate-open-human", Type: DepParentChild}, // not a blocks edge
	}
	got := humanGateBlocked(deps, idx)
	want := map[string]bool{"bead-a": true}
	if len(got) != len(want) || !got["bead-a"] {
		t.Errorf("humanGateBlocked = %v, want %v", got, want)
	}
}

// TestLanesRecognizesBdGate pins bw-avk's three acceptance criteria directly:
// a bead blocked by an open human gate (no label) reads as LaneWaiting; the
// same bead with the gate closed reads as LaneOpen; a bead carrying only the
// legacy "human" label still reads as LaneWaiting — the two signals are OR'd,
// never swapped.
func TestLanesRecognizesBdGate(t *testing.T) {
	t.Run("open gate, no label -> waiting", func(t *testing.T) {
		issues := []Issue{
			{ID: "bead", Status: StatusOpen},
			{ID: "gate", Status: StatusOpen, IssueType: "gate", AwaitType: "human"},
		}
		deps := []DepEdge{{IssueID: "bead", DependsOnID: "gate", Type: DepBlocks}}
		lanes := Lanes(issues, deps)
		if lanes["bead"] != LaneWaiting {
			t.Errorf("lanes[bead] = %v, want LaneWaiting", lanes["bead"])
		}
	})
	t.Run("closed gate -> blocks nothing", func(t *testing.T) {
		issues := []Issue{
			{ID: "bead", Status: StatusOpen},
			{ID: "gate", Status: StatusClosed, IssueType: "gate", AwaitType: "human"},
		}
		deps := []DepEdge{{IssueID: "bead", DependsOnID: "gate", Type: DepBlocks}}
		lanes := Lanes(issues, deps)
		if lanes["bead"] != LaneOpen {
			t.Errorf("lanes[bead] = %v, want LaneOpen", lanes["bead"])
		}
	})
	t.Run("human label alone still routes to waiting", func(t *testing.T) {
		issues := []Issue{
			{ID: "bead", Status: StatusOpen, Labels: []string{"human"}},
		}
		lanes := Lanes(issues, nil)
		if lanes["bead"] != LaneWaiting {
			t.Errorf("lanes[bead] = %v, want LaneWaiting", lanes["bead"])
		}
	})
}
