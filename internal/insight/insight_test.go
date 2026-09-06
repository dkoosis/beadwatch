package insight

import (
	"testing"

	"github.com/dkoosis/beadwatch/internal/bd"
	"github.com/dkoosis/beadwatch/pulse"
)

// TestConstantParity pins the one coupling this adapter cannot express in the
// type system: pulse spells bd's status and dep-type values a second time so
// that importing pulse never drags bd along, and Lanes crosses the gap with an
// unchecked string conversion. Change a spelling on either side and the
// conversion still compiles — the bead just falls out of its lane as an
// unknown status. This test is the compile error we don't get.
func TestConstantParity(t *testing.T) {
	statuses := map[bd.Status]pulse.Status{
		bd.StatusOpen:       pulse.StatusOpen,
		bd.StatusInProgress: pulse.StatusInProgress,
		bd.StatusBlocked:    pulse.StatusBlocked,
		bd.StatusClosed:     pulse.StatusClosed,
		bd.StatusDeferred:   pulse.StatusDeferred,
	}
	for got, want := range statuses {
		if string(got) != string(want) {
			t.Errorf("bd.Status %q != pulse.Status %q", got, want)
		}
	}
	deps := map[bd.DepType]pulse.DepType{
		bd.DepBlocks:      pulse.DepBlocks,
		bd.DepParentChild: pulse.DepParentChild,
		bd.DepRelatesTo:   pulse.DepRelatesTo,
	}
	for got, want := range deps {
		if string(got) != string(want) {
			t.Errorf("bd.DepType %q != pulse.DepType %q", got, want)
		}
	}
}

// TestLanesAdapter exercises the bd -> pulse conversion this package exists
// to do: every field pulse.Lanes reads (ID, Status, Labels, Metadata off
// bd.Issue; IssueID, DependsOnID, Type off bd.DepEdge) has to survive the
// trip unchanged for the partition itself to be correct. The lane-derivation
// logic this fixture exercises — human-gate wins, dependency blocking, the
// closed/deferred filter — is pulse's own six-row derivation table
// (pulse.TestLaneOfDerivationTable) and is not re-proved here.
func TestLanesAdapter(t *testing.T) {
	issues := []bd.Issue{
		{ID: "open", Status: bd.StatusOpen},
		{ID: "gated", Status: bd.StatusOpen, Labels: []string{"human"}},
		{ID: "review", Status: bd.StatusOpen, Metadata: map[string]any{"review_needed": "true"}},
		{ID: "blocker", Status: bd.StatusOpen},
		{ID: "depblocked", Status: bd.StatusOpen},
		{ID: "active", Status: bd.StatusInProgress},
		{ID: "storedblocked", Status: bd.StatusBlocked},
		{ID: "closed", Status: bd.StatusClosed},
		{ID: "deferred", Status: bd.StatusDeferred},
	}
	deps := []bd.DepEdge{
		{IssueID: "depblocked", DependsOnID: "blocker", Type: bd.DepBlocks},
		// parent-child is not a blocking edge — filtered out on both sides.
		{IssueID: "depblocked", DependsOnID: "closed", Type: bd.DepParentChild},
	}

	got := Lanes(issues, deps)
	want := map[string]Lane{
		"open":          LaneOpen,
		"gated":         LaneWaiting,
		"review":        LaneWaiting,
		"blocker":       LaneOpen,
		"depblocked":    LaneBlocked,
		"active":        LaneInProgress,
		"storedblocked": LaneBlocked,
		// closed, deferred → LaneNone, omitted from the map
	}
	for id, want := range want {
		if g := got[id]; g != want {
			t.Errorf("Lanes[%q] = %v, want %v", id, g, want)
		}
	}
	for _, none := range []string{"closed", "deferred"} {
		if _, ok := got[none]; ok {
			t.Errorf("Lanes[%q] present, want omitted (LaneNone)", none)
		}
	}
}
