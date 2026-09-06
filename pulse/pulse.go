// Package pulse computes a bead's disjoint pulse lane — the ○/◐/●/◆ four-way
// partition the counts derivation folds into bh/bo/bw/bb. It takes issues and
// dependency edges as plain data it owns (Issue, DepEdge), never bd's own
// bd.Issue/bd.DepEdge, so importing pulse never drags bd's client/store
// surface into a consumer — pulse itself never touches bd or HTTP.
//
// The code was lifted from strand's internal/insight (whose Model/Compute
// machinery serves the dashboard and pulls in internal/graph and
// internal/strand); only the lane partition — Lanes, laneOf, isHumanGated, and
// what they need — came along. This package is now the one home for it, and
// the direction of the dependency reverses: strand imports pulse rather than
// keeping a copy (bw-4id.1), and beadwatch's own internal/insight is a thin
// adapter that converts []bd.Issue/[]bd.DepEdge into pulse's input types and
// calls Lanes. One derivation, one brain (decision 251431366484).
package pulse

import "slices"

// humanLabel is the bd label marking a bead as parked on a human decision —
// bdx's DECISION queue convention (the human-gate model).
const humanLabel = "human"

// reviewNeededKey is the bd metadata key marking a bead as awaiting human
// review — bdx's REVIEW queue. bd emits it as the string "true"; bool is
// tolerated defensively.
const reviewNeededKey = "review_needed"

// humanGate classifies a bead's human-gate state from its full issue record: a
// DECISION (carries the "human" label) or a REVIEW (review_needed=="true"). A
// bead carrying both is a decision — the stronger "needs a human call" signal.
// A bead with neither is neither (claimable).
func humanGate(iss *Issue) (decision, review bool) {
	if iss == nil {
		return false, false
	}
	if slices.Contains(iss.Labels, humanLabel) {
		return true, false
	}
	if reviewNeeded(iss.Metadata) {
		return false, true
	}
	return false, false
}

// isHumanGated reports whether a bead is parked on a human (decision or
// review) — the gate arm of laneOf.
func isHumanGated(iss *Issue) bool {
	d, r := humanGate(iss)
	return d || r
}

// reviewNeeded reads metadata.review_needed. bd emits the flag as the string
// "true"; a bool true is tolerated in case a future bd quotes it differently.
// Anything else is "not flagged".
func reviewNeeded(m map[string]any) bool {
	switch v := m[reviewNeededKey].(type) {
	case string:
		return v == "true"
	case bool:
		return v
	default:
		return false
	}
}

// Lane is a bead's disjoint pulse lane — the four-way partition a status line
// reads to orient (○ Open / ◐ InProgress / ● Blocked / ◆ Waiting). Exactly one
// lane per live bead; LaneNone means the bead is not live work (closed or
// deferred). Decision 477486825755: human wins every overlap — a gated bead is
// LaneWaiting whatever its status or dependencies, so LaneInProgress means
// "in progress and NOT gated" (a gated in-progress bead is LaneWaiting only,
// never double-counted into both).
type Lane uint8

const (
	LaneNone       Lane = iota
	LaneOpen            // ○ actionable now
	LaneInProgress      // ◐ claimed, not gated
	LaneBlocked         // ● held by an unmet blocker (or stored "blocked")
	LaneWaiting         // ◆ parked on a human
)

// laneOf is the single precedence kernel Lanes runs, per decision
// 477486825755: gated beats blocked beats open/in-progress. A gated bead is
// LaneWaiting regardless of status or dependencies — a human call outranks
// everything else a bead could be waiting on. Only once gated is ruled out
// does status/blocker decide: a stored "blocked" status or an open bead with
// an unmet blocker is LaneBlocked; an ungated in-progress bead is
// LaneInProgress; a plain open bead is LaneOpen.
func laneOf(status Status, gated, hasBlocker bool) Lane {
	if status == StatusClosed || status == StatusDeferred {
		return LaneNone // not live work
	}
	if gated {
		return LaneWaiting // human wins every overlap — status and blocker moot
	}
	switch status {
	case StatusBlocked:
		return LaneBlocked
	case StatusInProgress:
		return LaneInProgress
	case StatusOpen:
		if hasBlocker {
			return LaneBlocked
		}
		return LaneOpen
	}
	return LaneNone // unknown/future status — defensive
}

// Lanes assigns every issue to its disjoint pulse lane, repo-wide, from the
// one laneOf precedence. deps carry the blocker signal; nil deps ⇒ no bead is
// dependency-blocked (a cold-cache path). LaneNone beads (closed/deferred) are
// omitted, so a missing key reads back as LaneNone (its zero value). Exactly
// one lane per included issue, so a count of a lane and a list of that lane's
// members agree by construction, and bh+bo+bw+bb sums to the count of beads
// whose status is open, in_progress or blocked.
func Lanes(issues []Issue, deps []DepEdge) map[string]Lane {
	idx := indexIssues(issues)
	openBlockers := blockerCounts(deps, idx)
	lanes := make(map[string]Lane, len(issues))
	for i := range issues {
		iss := &issues[i]
		if l := laneOf(iss.Status, isHumanGated(iss), openBlockers[iss.ID] > 0); l != LaneNone {
			lanes[iss.ID] = l
		}
	}
	return lanes
}

// indexIssues maps every repo bead by id.
func indexIssues(issues []Issue) map[string]Issue {
	m := make(map[string]Issue, len(issues))
	for i := range issues {
		m[issues[i].ID] = issues[i]
	}
	return m
}

// blockerCounts tallies, per bead, how many of its blocks-dependencies are
// still unmet. A blocker counts only if it's present in the live index AND
// not closed; an absent target is treated as resolved, since `bd list` omits
// closed beads (a done dependency simply isn't in the list).
func blockerCounts(deps []DepEdge, idx map[string]Issue) map[string]int {
	open := map[string]int{}
	for _, d := range deps {
		if d.Type != DepBlocks {
			continue
		}
		if iss, ok := idx[d.DependsOnID]; ok && iss.Status != StatusClosed {
			open[d.IssueID]++
		}
	}
	return open
}
