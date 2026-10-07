package counts

import (
	"os"
	"path/filepath"
	"strings"
)

// canonical returns root as the filesystem spells it: symlinks resolved and every
// component in its on-disk case (bw-55b). counts.json is keyed by repo root, and
// readers look a repo up by the path git reports — `git rev-parse --path-format=absolute`
// returns realpath(3), the on-disk spelling. On a case-insensitive volume (macOS
// default) a scan dir configured as ~/projects reaches a directory that is really
// ~/Projects; keyed by the configured spelling, every row missed every reader, and a
// repo reached by both spellings got two rows.
//
// Case is recovered by listing each parent and taking the entry whose name matches
// exactly, else the one that matches ignoring case. A component that cannot be
// listed or matched is kept as given, so a path absent from this machine comes
// back unchanged and pruneAbsent still names it as the caller wrote it.
func canonical(root string) string {
	p := filepath.Clean(root)
	if !filepath.IsAbs(p) {
		return p
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	parts := strings.Split(strings.TrimPrefix(p, string(filepath.Separator)), string(filepath.Separator))
	out := string(filepath.Separator)
	for i, part := range parts {
		name, ok := onDiskName(out, part)
		if !ok {
			return filepath.Join(append([]string{out}, parts[i:]...)...)
		}
		out = filepath.Join(out, name)
	}
	return out
}

// onDiskName returns the entry of dir that part names: the exact match if one
// exists (a case-sensitive volume may hold both Foo and foo), else the sole
// case-insensitive match.
func onDiskName(dir, part string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	folded := ""
	for _, e := range entries {
		switch {
		case e.Name() == part:
			return part, true
		case folded == "" && strings.EqualFold(e.Name(), part):
			folded = e.Name()
		}
	}
	return folded, folded != ""
}

// canonicalKeys re-keys a map loaded from disk by canonical root. A file written
// before bw-55b can hold one repo under a non-canonical spelling, or under two;
// the entry already keyed canonically wins, so a re-key never clobbers a fresher row.
func canonicalKeys[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m))
	var moved []string
	for k, v := range m {
		if canonical(k) == k {
			out[k] = v
		} else {
			moved = append(moved, k)
		}
	}
	for _, k := range moved {
		c := canonical(k)
		if _, ok := out[c]; !ok {
			out[c] = m[k]
		}
	}
	return out
}
