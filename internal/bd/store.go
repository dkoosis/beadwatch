package bd

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
)

// manifestPaths lists the repo's Dolt manifest files. A glob error (a repoPath
// holding a malformed pattern character) means no usable match: StoreContentKey's
// degrade-to-caller-behavior signal, same as an empty result.
func manifestPaths(repoPath string) []string {
	matches, err := filepath.Glob(filepath.Join(repoPath, ".beads", "embeddeddolt", "*", ".dolt", "noms", "manifest"))
	if err != nil {
		return nil
	}
	return matches
}

// StoreContentKey reports a content-derived key for the repo's Dolt noms
// manifest(s), an out-of-band change signal: FNV-1a 64 folded over each
// manifest's (path, content) pair, sorted by path for a deterministic key
// regardless of glob order. It does NOT move when a pure bd read rewrites the
// manifest's mtime with identical bytes (every bd read touches the mtime, so
// gating on mtime makes the refresher think every repo just changed). Content
// still moves on any genuine write: a local bd commit or an out-of-band
// `bd dolt pull/sync`, `bd import`.
//
// Read-only: it opens the tiny (~183B) manifest metadata file to hash its
// bytes, and never opens the Dolt store's data tables, inside bd's "never touch
// Dolt directly" contract. ok is false when nothing matches or every read
// fails: the caller keeps its pre-gate behavior.
func StoreContentKey(repoPath string) (int64, bool) {
	matches := manifestPaths(repoPath)
	sort.Strings(matches)
	h := fnv.New64a()
	found := false
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		found = true
		_, _ = h.Write([]byte(m))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	if !found {
		return 0, false
	}
	return int64(h.Sum64()), true //nolint:gosec // G115: opaque hash key, sign/truncation is not meaningful
}
