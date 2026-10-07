package counts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/beadwatch/internal/bdcounts"
)

// realTempDir is t.TempDir as canonical() spells it — macOS's temp dir sits behind
// the /var → /private/var symlink, and every key beadwatch writes is resolved.
func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return d
}

// caseInsensitive skips a test on a case-sensitive volume, where a wrong-case
// spelling names no directory and there is nothing to recover.
func caseInsensitive(t *testing.T, dir string) {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	if _, err := os.Stat(strings.ToLower(probe)); err != nil {
		t.Skip("case-sensitive volume: no wrong-case spelling to canonicalize")
	}
}

func TestCanonicalRecoversOnDiskCase(t *testing.T) {
	base := realTempDir(t)
	caseInsensitive(t, base)
	want := mkRepo(t, filepath.Join(base, "Projects"), "Repo")
	got := canonical(filepath.Join(base, "projects", "repo"))
	if got != want {
		t.Errorf("canonical = %q, want %q", got, want)
	}
}

func TestCanonicalResolvesSymlinks(t *testing.T) {
	base := realTempDir(t)
	want := mkRepo(t, base, "real")
	link := filepath.Join(base, "link")
	if err := os.Symlink(want, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if got := canonical(link); got != want {
		t.Errorf("canonical = %q, want %q", got, want)
	}
}

// TestCanonicalKeepsWrongCaseOnCaseSensitiveVolume: where foo is absent and Foo
// is real, foo is a different path and must not be folded onto Foo.
func TestCanonicalKeepsWrongCaseOnCaseSensitiveVolume(t *testing.T) {
	base := realTempDir(t)
	mkRepo(t, base, "Foo")
	lower := filepath.Join(base, "foo")
	if _, err := os.Stat(lower); err == nil {
		t.Skip("case-insensitive volume: foo is Foo here")
	}
	if got := canonical(lower); got != lower {
		t.Errorf("canonical = %q, want %q unchanged", got, lower)
	}
}

func TestCanonicalKeepsAbsentPath(t *testing.T) {
	absent := filepath.Join(realTempDir(t), "gone", "repo")
	if got := canonical(absent); got != absent {
		t.Errorf("canonical = %q, want %q unchanged", got, absent)
	}
}

// TestRefreshKeysByOnDiskCase is the bw-55b regression: a scan dir configured in
// the wrong case keyed every row by that spelling, so the status line (which looks
// up git's on-disk spelling) found nothing. A row already on disk under the wrong
// spelling is re-keyed, not left as a duplicate.
func TestRefreshKeysByOnDiskCase(t *testing.T) {
	base := realTempDir(t)
	caseInsensitive(t, base)
	cache := t.TempDir()
	want := mkRepo(t, filepath.Join(base, "Projects"), "repo-a")
	wrong := filepath.Join(base, "projects", "repo-a")

	outPath := filepath.Join(cache, "counts.json")
	if err := writeRowsAtomic(outPath, map[string]Row{wrong: {Root: wrong, BO: 9}}, bdcounts.Meta{}); err != nil {
		t.Fatalf("seed counts.json: %v", err)
	}

	cfg := config{
		cacheDir:  cache,
		projects:  filepath.Join(base, "projects"),
		mode:      modeAll,
		newSource: func(string) source { return oneOpenBead() },
	}
	if err := refresh(context.Background(), &cfg); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	rows := map[string]Row{}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read counts.json: %v", err)
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("parse counts.json: %v", err)
	}
	delete(rows, bdcounts.MetaKey)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly one, keyed %q", keys(rows), want)
	}
	row, ok := rows[want]
	if !ok {
		t.Fatalf("rows = %v, want key %q", keys(rows), want)
	}
	if row.Root != want || row.BO != 1 {
		t.Errorf("row = {Root:%q BO:%d}, want {Root:%q BO:1}", row.Root, row.BO, want)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
