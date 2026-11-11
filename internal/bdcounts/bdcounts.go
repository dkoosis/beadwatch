// Package bdcounts reads the shared bead-count cache that the off-render launchd
// agent com.trixi.beadwatch writes (scripts/tool-install-launchd.sh authors the plist).
// That agent is the ONE place bead counts are derived; the Claude Code status line
// and strand's masthead pulse are both dumb readers of the same file, so the two
// surfaces can never disagree (st-p1f).
//
// History: com.trixi.bd-counts ran cc-plugins' bd-counts-refresh.sh here first.
// History: `strand counts` wrote it next. beadwatch replaced both (bw-4id).
//
// The file is a JSON object keyed by each repo's absolute root path:
//
//	{"<repo_root>": {"prefix":..,"bh":..,"bo":..,"bw":..,"bb":..,"bcl":..,"bdf":..,"ts":..}}
//
// where the buckets map onto strand's Pulse glyphs: bh=◆ ready∧human, bo=○ ready∧
// ¬human, bw=◐ in_progress, bb=● blocked, bcl=✓ closed, bdf=❄ deferred. strand
// reads only these six aggregates; the board/graph/detail views stay on bd.
package bdcounts

// MetaKey is the reserved top-level key in counts.json under which the refresher
// stamps its liveness meta (last-run time + binary version). It is not a repo root
// — every real key is an absolute path — so keyed readers (Lookup, the shell status
// line's `.[$repo]`) never collide with it, and a value shaped unlike an entry
// unmarshals into a zero entry there, harmless. It lets a dead or wedged refresher
// be seen (st-18l/B4) instead of freezing counts.json indistinguishably from "nothing
// changed."
const MetaKey = "_meta"

// Meta is the refresher's liveness stamp, written under MetaKey. LastRun is the unix
// second the last `beadwatch` run finished; Version is the binary that wrote it, so a
// stale ~/go/bin/beadwatch reintroducing predicate drift (B2) is legible in the file.
type Meta struct {
	LastRun int64  `json:"lastRun"`
	Version string `json:"version"`
}
