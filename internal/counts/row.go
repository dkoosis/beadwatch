package counts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dkoosis/beadwatch/internal/bdcounts"
)

// errRowUsage is returned when `beadwatch row` is not given exactly one dir.
var errRowUsage = errors.New("usage: beadwatch row <dir>")

// RunRow is the `beadwatch row <dir>` entry point (bw-ryi): it prints the counts.json
// row that belongs to dir, so every status-line renderer resolves "which row is mine"
// the same way instead of each re-deriving it.
//
// It is a dumb reader (sd-3wp): it reads counts.json and runs `git rev-parse`, and
// nothing else — it never spawns bd, never refreshes, never takes the refresh lock,
// and never writes. A dir with no row (not a git repo, or no repo row cached) prints
// nothing and returns nil; an unreadable or corrupt counts.json is an error.
func RunRow(args []string, w io.Writer) error {
	if len(args) != 1 {
		return errRowUsage
	}
	return row(context.Background(), args[0], rowsFile(), w) //nolint:forbidigo // CLI verb root: `beadwatch row` is a one-shot command, not request-scoped
}

// row writes dir's row to w exactly as stored in path. Candidates are tried
// own-row-first: the checkout dir sits in (a linked worktree's own row), then the
// main checkout that owns its git common dir.
func row(ctx context.Context, dir, path string, w io.Writer) error {
	cands := rowCandidates(ctx, dir)
	if len(cands) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("row: read counts: %w", err)
	}
	var rows map[string]json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		return fmt.Errorf("row: parse %s: %w", path, err)
	}
	delete(rows, bdcounts.MetaKey)
	rows = canonicalKeys(rows) // a pre-bw-55b file may key a repo by another spelling
	for _, c := range cands {
		if raw, ok := rows[c]; ok {
			if _, err := fmt.Fprintf(w, "%s\n", raw); err != nil {
				return fmt.Errorf("row: write: %w", err)
			}
			return nil
		}
	}
	return nil
}

// rowCandidates lists the roots whose row could be dir's, in lookup order, each
// canonicalized the way the writer keys rows. Not in a git work tree → none.
func rowCandidates(ctx context.Context, dir string) []string {
	top := gitPath(ctx, dir, "--show-toplevel")
	if top == "" {
		return nil
	}
	cands := []string{canonical(top)}
	common := gitPath(ctx, dir, "--path-format=absolute", "--git-common-dir")
	if filepath.Base(common) == ".git" {
		if mainRoot := canonical(filepath.Dir(common)); mainRoot != cands[0] {
			cands = append(cands, mainRoot)
		}
	}
	return cands
}

// gitPath runs `git -C dir rev-parse <args>` and returns its trimmed stdout, or ""
// when git fails (dir absent, not a repo, git missing).
func gitPath(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "rev-parse"}, args...)...) //nolint:gosec // fixed binary; dir is the caller's own path argument
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// rowsFile resolves the counts.json a reader consults: WRAP_COUNTS_FILE (the
// consumer-test seam), else the writer's cache dir (BD_COUNTS_CACHE_DIR, else
// ~/.cache/cc-dashboard).
func rowsFile() string {
	if f := os.Getenv("WRAP_COUNTS_FILE"); f != "" {
		return f
	}
	return filepath.Join(cacheDir(), "counts.json")
}
