package insight

import (
	"testing"

	"github.com/dkoosis/beadwatch/internal/bd"
)

// TestLaneOfDerivationTable pins the six rows of the design's §The derivation
// table (~/Projects/kg/Project/beadwatch/specs/beadwatch-design.md), "after"
// column — decision 477486825755: gated beats blocked beats open/in-progress,
// whatever the status or dependencies.
func TestLaneOfDerivationTable(t *testing.T) {
	cases := []struct {
		name              string
		status            bd.Status
		gated, hasBlocker bool
		want              Lane
	}{
		{"open, human label, open dependency -> bh", bd.StatusOpen, true, true, LaneWaiting},
		{"in_progress, human label -> bh only", bd.StatusInProgress, true, false, LaneWaiting},
		{"status blocked, human label -> bh", bd.StatusBlocked, true, false, LaneWaiting},
		{"open, open dependency -> bb", bd.StatusOpen, false, true, LaneBlocked},
		{"in_progress, no label -> bw", bd.StatusInProgress, false, false, LaneInProgress},
		{"open, plain -> bo", bd.StatusOpen, false, false, LaneOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := laneOf(c.status, c.gated, c.hasBlocker); got != c.want {
				t.Errorf("laneOf(%v, gated=%v, hasBlocker=%v) = %v, want %v", c.status, c.gated, c.hasBlocker, got, c.want)
			}
		})
	}
}

// TestPartitionSumsToLiveBeads is the partition property decision 477486825755
// promises: bh+bo+bw+bb equals the count of beads whose status is open,
// in_progress or blocked — nothing live is dropped and nothing is double
// counted. The fixture carries every gate/status/blocker overlap. Against the
// pre-change laneOf this is red: an ungated in_progress bead (plain-inprogress)
// used to map to LaneNone (a raw ◐ count kept outside the partition), so it
// vanished from the sum instead of landing in bw.
func TestPartitionSumsToLiveBeads(t *testing.T) {
	issues := []bd.Issue{
		{ID: "plain-open", Status: bd.StatusOpen},
		{ID: "blocker", Status: bd.StatusOpen},
		{ID: "open-blocked", Status: bd.StatusOpen},
		{ID: "gated-open-blocked", Status: bd.StatusOpen, Labels: []string{"human"}},
		{ID: "review-open", Status: bd.StatusOpen, Metadata: map[string]any{"review_needed": "true"}},
		{ID: "plain-inprogress", Status: bd.StatusInProgress},
		{ID: "gated-inprogress", Status: bd.StatusInProgress, Labels: []string{"human"}},
		{ID: "plain-blocked-status", Status: bd.StatusBlocked},
		{ID: "gated-blocked-status", Status: bd.StatusBlocked, Labels: []string{"human"}},
		{ID: "closed", Status: bd.StatusClosed},
		{ID: "deferred", Status: bd.StatusDeferred},
	}
	deps := []bd.DepEdge{
		{IssueID: "open-blocked", DependsOnID: "blocker", Type: bd.DepBlocks},
		{IssueID: "gated-open-blocked", DependsOnID: "blocker", Type: bd.DepBlocks},
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
		case bd.StatusOpen, bd.StatusInProgress, bd.StatusBlocked:
			wantTotal++
		}
	}

	if got := bh + bo + bw + bb; got != wantTotal {
		t.Errorf("bh+bo+bw+bb = %d (bh=%d bo=%d bw=%d bb=%d), want %d — the count of open/in_progress/blocked beads",
			got, bh, bo, bw, bb, wantTotal)
	}
}

func TestLanes(t *testing.T) {
	issues := []bd.Issue{
		{ID: "open", Status: bd.StatusOpen},
		{ID: "gated", Status: bd.StatusOpen, Labels: []string{"human"}},
		{ID: "depblocked", Status: bd.StatusOpen},
		{ID: "depblockedgated", Status: bd.StatusOpen, Labels: []string{"human"}}, // ◆ — gated wins over the blocker
		{ID: "storedblocked", Status: bd.StatusBlocked},
		{ID: "storedblockedgated", Status: bd.StatusBlocked, Labels: []string{"human"}}, // ◆ — gated wins over the stored status
		{ID: "blocker", Status: bd.StatusOpen},
		{ID: "activegated", Status: bd.StatusInProgress, Labels: []string{"human"}}, // ◆
		{ID: "active", Status: bd.StatusInProgress},                                 // ◐ — LaneInProgress, not omitted
		{ID: "closed", Status: bd.StatusClosed},
		{ID: "deferred", Status: bd.StatusDeferred},
	}
	deps := []bd.DepEdge{
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

func assertLanes(t *testing.T, got, want map[string]Lane, issues []bd.Issue) {
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
		iss                      bd.Issue
		wantDecision, wantReview bool
	}{
		{"plain", bd.Issue{ID: "a"}, false, false},
		{"label human", bd.Issue{Labels: []string{"core", "human"}}, true, false},
		{"review string true", bd.Issue{Metadata: map[string]any{"review_needed": "true"}}, false, true},
		{"review bool true", bd.Issue{Metadata: map[string]any{"review_needed": true}}, false, true},
		{"review false", bd.Issue{Metadata: map[string]any{"review_needed": "false"}}, false, false},
		{"both decision wins", bd.Issue{Labels: []string{"human"}, Metadata: map[string]any{"review_needed": "true"}}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, r := humanGate(&c.iss)
			if d != c.wantDecision || r != c.wantReview {
				t.Errorf("humanGate = (%v,%v), want (%v,%v)", d, r, c.wantDecision, c.wantReview)
			}
		})
	}
}
