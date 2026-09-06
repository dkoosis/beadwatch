package bd

// Status is a bead's lifecycle state — the domain vocabulary the lane partition
// and the counts derivation reason about. bd is the source of these values, so
// the named type and its closed set live here; a typo or a drift from bd's
// emitted spelling is a compile error, not a silent miss.
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
// (DepRelatesTo) are filtered out. bd owns this closed set.
type DepType string

const (
	DepBlocks      DepType = "blocks"
	DepParentChild DepType = "parent-child"
	DepRelatesTo   DepType = "relates_to"
)
