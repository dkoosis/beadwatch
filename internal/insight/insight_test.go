package insight

import (
	"testing"

	"github.com/dkoosis/beadwatch/internal/bd"
)

func TestLanes(t *testing.T) {
	issues := []bd.Issue{
		{ID: "open", Status: bd.StatusOpen},
		{ID: "gated", Status: bd.StatusOpen, Labels: []string{"human"}},
		{ID: "depblocked", Status: bd.StatusOpen},
		{ID: "depblockedgated", Status: bd.StatusOpen, Labels: []string{"human"}}, // ● not ◆
		{ID: "storedblocked", Status: bd.StatusBlocked},
		{ID: "storedblockedgated", Status: bd.StatusBlocked, Labels: []string{"human"}}, // ● not ◆
		{ID: "blocker", Status: bd.StatusOpen},
		{ID: "activegated", Status: bd.StatusInProgress, Labels: []string{"human"}}, // ◆
		{ID: "active", Status: bd.StatusInProgress},                                 // ◐ → None
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
			"depblocked":         LaneBlocked,
			"depblockedgated":    LaneBlocked, // blocker beats gate
			"storedblocked":      LaneBlocked,
			"storedblockedgated": LaneBlocked, // blocker beats gate
			// active, closed, deferred → LaneNone, omitted from the map
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
			"depblocked":         LaneOpen,    // no deps → not dep-blocked
			"depblockedgated":    LaneWaiting, // no deps → gate now wins
			"storedblocked":      LaneBlocked, // stored status needs no deps
			"storedblockedgated": LaneBlocked, // stored status still beats gate cold
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
