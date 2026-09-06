// Package insight adapts beadwatch's own bd.Issue/bd.DepEdge to the
// github.com/dkoosis/beadwatch/pulse package's plain input types and calls
// pulse.Lanes for the four-way ○/◐/●/◆ partition the counts derivation folds
// into bh/bo/bw/bb. The partition logic itself — Lanes, laneOf, isHumanGated,
// and the rest of the derivation kernel — lives in pulse, exported outside
// internal/ so strand can import it directly instead of carrying its own copy
// (decision 251431366484: one derivation, one brain). This package holds only
// the bd-shaped conversion; Lane and its five values are re-exported from
// pulse unchanged so every existing caller in this repo (internal/counts)
// keeps working without a rename.
package insight

import (
	"github.com/dkoosis/beadwatch/internal/bd"
	"github.com/dkoosis/beadwatch/pulse"
)

// Lane is beadwatch's alias for pulse.Lane — the disjoint pulse lane a status
// line reads to orient (○ Open / ◐ InProgress / ● Blocked / ◆ Waiting). See
// pulse.Lane's doc comment for the full partition semantics, including
// decision 477486825755 (human wins every overlap).
type Lane = pulse.Lane

// The five Lane values, re-exported from pulse so callers of this package
// never need to import pulse directly.
const (
	LaneNone       = pulse.LaneNone
	LaneOpen       = pulse.LaneOpen
	LaneInProgress = pulse.LaneInProgress
	LaneBlocked    = pulse.LaneBlocked
	LaneWaiting    = pulse.LaneWaiting
)

// Lanes converts issues and dependency edges from bd's wire shapes into
// pulse's plain input types and delegates to pulse.Lanes. deps carry the
// blocker signal; nil deps ⇒ no bead is dependency-blocked (a cold-cache
// path). See pulse.Lanes for the full partition contract.
func Lanes(issues []bd.Issue, deps []bd.DepEdge) map[string]Lane {
	pIssues := make([]pulse.Issue, len(issues))
	for i := range issues {
		pIssues[i] = pulse.Issue{
			ID:       issues[i].ID,
			Status:   pulse.Status(issues[i].Status),
			Labels:   issues[i].Labels,
			Metadata: issues[i].Metadata,
		}
	}
	pDeps := make([]pulse.DepEdge, len(deps))
	for i := range deps {
		pDeps[i] = pulse.DepEdge{
			IssueID:     deps[i].IssueID,
			DependsOnID: deps[i].DependsOnID,
			Type:        pulse.DepType(deps[i].Type),
		}
	}
	return pulse.Lanes(pIssues, pDeps)
}
