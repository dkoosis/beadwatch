// Command beadwatch derives the shared bead-count cache (counts.json) that the
// Claude Code status line and other readers consume, off any render path. It
// is a standalone extraction of strand's `strand counts` subcommand
// (bw-4id/bw-4wu): a beadwatch binary writes counts.json in strand's place.
//
// Usage mirrors `strand counts` exactly, minus the subcommand word — this
// binary's one job is counts, so its args ARE counts' flags:
//
//	beadwatch              # every changed repo (skip mtime-unchanged)
//	beadwatch --all        # every discovered repo, unconditionally
//	beadwatch <dir>...      # only the named repo roots
//	beadwatch row <dir>     # print dir's counts.json row (read-only; bw-ryi)
//
// The leading `row` verb is matched before any flag or dir: targets are absolute
// repo roots, so a dir literally named row is a non-case.
//
// Env: BD_COUNTS_CACHE_DIR, BD_COUNTS_PROJECTS_DIR (read inside internal/counts).
package main

import (
	"log"
	"os"

	"github.com/dkoosis/beadwatch/internal/counts"
)

// Version is stamped at build time via -ldflags '-X main.Version=...' (see
// the Makefile).
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "row" {
		if err := counts.RunRow(os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("beadwatch: %v", err)
		}
		return
	}
	if err := counts.Run(os.Args[1:], Version); err != nil {
		log.Fatalf("beadwatch: %v", err)
	}
}
