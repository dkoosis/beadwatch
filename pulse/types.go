package pulse

// Status is a bead's lifecycle state, as the lane partition needs it. bd
// (github.com/dkoosis/beadwatch/internal/bd) is the source of these values;
// pulse mirrors bd's wire spelling here instead of importing bd.Status, so
// that a package importing pulse never drags bd's client/store surface along.
// The adapter converts with an unchecked string conversion, so a drift from
// bd's spelling would land silently as an unknown status (LaneNone), not as a
// build failure — internal/insight's TestConstantParity is what turns that
// drift into a test failure.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusClosed     Status = "closed"
	StatusDeferred   Status = "deferred"
)

// DepType is a dependency edge's kind. The lane partition keeps only the real
// blocking edge (DepBlocks); epic hierarchy (DepParentChild) and soft links
// (DepRelatesTo) are filtered out. Mirrors bd.DepType's wire spelling without
// importing bd, for the same reason as Status above.
type DepType string

const (
	DepBlocks      DepType = "blocks"
	DepParentChild DepType = "parent-child"
	DepRelatesTo   DepType = "relates_to"
)

// Issue is the plain input Lanes partitions from — the bd.Issue fields the
// lane derivation actually reads (ID, Status, Labels, Metadata, IssueType,
// AwaitType), lifted into a type pulse owns so importing pulse doesn't drag
// in bd.Issue's much larger shape (Title, Priority, timestamps, and bd's
// client/store surface behind it). internal/insight is the adapter that
// builds these from a real bd.Issue.
type Issue struct {
	ID     string
	Status Status
	Labels []string
	// IssueType is bd's issue_type; "gate" identifies a formal wait-condition
	// issue created by `bd gate create`, distinguishing it from an ordinary
	// issue that happens to carry an AwaitType.
	IssueType string
	// AwaitType is set only on a gate issue (IssueType=="gate"): what it waits
	// on — "human", "timer", "gh:run", "gh:pr", or "bead". Only "human" routes
	// a bead this gate blocks into LaneWaiting (bw-avk).
	AwaitType string
	Metadata  map[string]any
}

// DepEdge is the plain input Lanes partitions dependency edges from — the
// three bd.DepEdge fields the lane derivation reads (IssueID, DependsOnID,
// Type), decoupled from bd.DepEdge the same way Issue is decoupled from
// bd.Issue.
type DepEdge struct {
	IssueID     string
	DependsOnID string
	Type        DepType
}
