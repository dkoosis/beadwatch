// Package strandmd resolves a repo's roadmap-ordered epic ids from its
// ROADMAP.md (or the legacy NORTH_STAR.md). It is the Go twin of
// roadmap-epics.sh's roadmap_file/roadmap_parse contract, reduced to that
// script's two non-kg branches.
//
// This is a deliberately narrow slice of strand's internal/strandmd (whose
// Load/Context machinery resolves the layered STRAND.md grounding context for
// an LLM drawer-assist call, unrelated to counts): only Roadmap comes along —
// the counts row reads "epics" and "roadmap" from it (bw-4wu design
// §Constraints: "strandmd.Roadmap comes along, because the banner reads epics
// and roadmap from the row").
package strandmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NorthStarFile is the repo-root file Roadmap falls back to when ROADMAP.md is
// absent — the legacy `## roadmap` pointer.
const NorthStarFile = "NORTH_STAR.md"

// RoadmapFile is the repo-root file holding the project's destination line and
// its ordered epic list — the sdlc standard. Resolved before the legacy
// NorthStarFile pointer.
const RoadmapFile = "ROADMAP.md"

// Roadmap returns the repo's roadmap-ordered epic ids. It is decision-owned
// order, not status; a station bar or pickNext cascade walks it against the
// live epic DAG.
//
// Resolution: $repoPath/ROADMAP.md, else the legacy $repoPath/NORTH_STAR.md.
//
// ROADMAP.md format: numbered lines carrying an arrow inside a `## Epics`
// section (legacy `## Milestones`/`## Route` headings tolerated) —
// `N. [status] <title> → <id>[, <id>...]`. The epic id is the first
// comma/whitespace-delimited token after the first arrow (a legacy multi-id
// line: first wins). A numbered line with no arrow is a stray non-epic list
// item and is skipped, not read as a blank id. An indented continuation line
// folds into its preceding numbered line before parsing, so a title too long
// for one line still yields one epic, not a mangled arrow-less row.
//
// NORTH_STAR.md, resolved only when ROADMAP.md is absent, keeps its own
// original `## roadmap` section + id-first-token parse (roadmapIDs below) as
// the final fallback — no `## Epics` heading, no arrow.
//
// A missing file, missing section, or empty section yields nil.
func Roadmap(repoPath string) []string {
	if repoPath == "" {
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(repoPath, RoadmapFile)); err == nil {
		return roadmapEpicIDs(string(b))
	}
	b, err := os.ReadFile(filepath.Join(repoPath, NorthStarFile))
	if err != nil {
		return nil
	}
	return roadmapIDs(string(b))
}

// epicSectionHeadings names the H2 headings that open ROADMAP.md's ordered
// epic list: the current name plus the two size-word legacy names a
// not-yet-conformed page may still carry.
var epicSectionHeadings = map[string]bool{
	"epics":      true,
	"milestones": true,
	"route":      true,
}

// roadmapEpicIDs extracts ROADMAP.md's ordered epic ids: fold the epics
// section's wrapped continuation lines into their numbered row
// (foldEpicSection), then keep each row's id only if the row carries an
// arrow (epicLineID).
func roadmapEpicIDs(s string) []string {
	var ids []string
	for _, row := range foldEpicSection(s) {
		if id, ok := epicLineID(row); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// foldEpicSection returns the epics section's numbered lines as one row
// apiece, with any immediately following indented continuation lines folded
// in — the Go twin of roadmap-epics.sh's awk fold. A numbered line starts a
// new row; an indented line with content extends the current row; anything
// else (blank line, unindented prose, a heading) ends the current row without
// extending it. Rows outside the epics section are dropped.
func foldEpicSection(s string) []string {
	var rows []string
	var cur strings.Builder
	inSection := false
	flush := func() {
		if cur.Len() > 0 {
			rows = append(rows, cur.String())
			cur.Reset()
		}
	}
	for ln := range strings.SplitSeq(s, "\n") {
		line := strings.TrimRight(ln, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case !inSection:
			if isH2(trimmed) && epicSectionHeadings[strings.ToLower(h2Name(trimmed))] {
				inSection = true
			}
		case isH2(trimmed):
			flush()
			return rows // next heading ends the section
		case isNumberedLine(trimmed):
			flush()
			cur.WriteString(trimmed)
		case isIndentedContinuation(line):
			if cur.Len() > 0 {
				cur.WriteByte(' ')
				cur.WriteString(trimmed)
			}
		default:
			flush() // blank line or unindented prose: ends the row, folds nothing
		}
	}
	flush()
	return rows
}

// isNumberedLine reports whether line starts with a leading integer and a dot
// (the epic-list row marker: "1.", "10.", …), independent of what follows.
func isNumberedLine(line string) bool {
	dot := strings.IndexByte(line, '.')
	if dot <= 0 {
		return false
	}
	_, err := strconv.Atoi(line[:dot])
	return err == nil
}

// isIndentedContinuation reports whether line is a wrapped-title
// continuation: it starts with leading whitespace AND has non-whitespace
// content after it. A whitespace-only line does not match, so it falls
// through to ending the row like any other blank line.
func isIndentedContinuation(line string) bool {
	if line == "" || (line[0] != ' ' && line[0] != '\t') {
		return false
	}
	return strings.TrimSpace(line) != ""
}

// epicLineID extracts a folded epic row's id: the row must carry an arrow
// (else a stray numbered line, e.g. from another section's own list, is not
// an epic), and the id is the first comma/whitespace-delimited token after
// the first arrow — a legacy line with several ids keeps only the first.
func epicLineID(row string) (string, bool) {
	_, after, ok := strings.Cut(row, "→")
	if !ok {
		return "", false
	}
	rest := strings.ReplaceAll(after, ",", " ")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], true
}

// roadmapIDs scans the `## roadmap` section for numbered lines
// (`N. <epic-id> …`) and returns each line's epic id, in order. It reads only
// within the section: the scan starts after the `## roadmap` heading and
// stops at the next level-2 heading.
func roadmapIDs(s string) []string {
	var ids []string
	inSection := false
	for ln := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case !inSection:
			if isH2(t) && strings.EqualFold(h2Name(t), "roadmap") {
				inSection = true
			}
		case isH2(t):
			return ids // next heading ends the section
		default:
			if id, ok := numberedEpicID(t); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// numberedEpicID pulls the epic id from a roadmap line of the form
// `N. <id> — …`: a leading number, a dot, then the id as the next
// whitespace-delimited token. Any line that isn't numbered (prose, blank)
// yields ok false.
func numberedEpicID(line string) (string, bool) {
	dot := strings.IndexByte(line, '.')
	if dot <= 0 {
		return "", false
	}
	if _, err := strconv.Atoi(line[:dot]); err != nil {
		return "", false
	}
	rest := strings.Fields(line[dot+1:])
	if len(rest) == 0 {
		return "", false
	}
	return rest[0], true
}

// isH2 reports whether ln is a level-2 ATX heading (## ...), not a deeper
// level.
func isH2(ln string) bool {
	t := strings.TrimSpace(ln)
	return strings.HasPrefix(t, "## ") && !strings.HasPrefix(t, "### ")
}

// h2Name returns an H2 heading's text, stripped of leading #s and surrounding
// whitespace.
func h2Name(ln string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), "#"))
}
