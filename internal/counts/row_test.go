package counts

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs git in dir with a fixed identity and no hooks (a global
// core.hooksPath would otherwise lint the fixture commit), failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + t.TempDir()}, args...)
	if out, err := exec.CommandContext(context.Background(), "git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repoWithWorktree builds a main checkout with one commit and a linked worktree
// beside it, returning both roots as canonical() spells them.
func repoWithWorktree(t *testing.T) (mainRoot, wt string) {
	t.Helper()
	base := realTempDir(t)
	mainRoot = filepath.Join(base, "main")
	wt = filepath.Join(base, "wt")
	if err := os.Mkdir(mainRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gitRun(t, mainRoot, "init", "-q")
	gitRun(t, mainRoot, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, mainRoot, "worktree", "add", "-q", wt)
	return mainRoot, wt
}

// writeFixture writes a counts.json holding rows keyed by root, each row a
// distinct bh so the test can tell which one came back.
func writeFixture(t *testing.T, path string, rows map[string]int) {
	t.Helper()
	m := map[string]any{"_meta": map[string]any{"version": "test"}}
	for root, bh := range rows {
		m[root] = map[string]any{"root": root, "bh": bh}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// runRow calls RunRow for dir and returns its stdout and error.
func runRow(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := RunRow([]string{dir}, &out)
	return out.String(), err
}

// rowBH decodes a printed row's bh.
func rowBH(t *testing.T, out string) int {
	t.Helper()
	var r struct {
		BH int `json:"bh"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("decode row %q: %v", out, err)
	}
	return r.BH
}

func TestRowMainCheckoutGetsItsRow(t *testing.T) {
	mainRoot, wt := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{mainRoot: 1, wt: 2})
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, mainRoot)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 1 {
		t.Errorf("bh = %d, want 1 (main's row)", got)
	}
}

func TestRowWorktreeWithOwnRowGetsOwnRow(t *testing.T) {
	mainRoot, wt := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{mainRoot: 1, wt: 2})
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, wt)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 2 {
		t.Errorf("bh = %d, want 2 (worktree's own row)", got)
	}
}

func TestRowWorktreeWithoutRowFallsBackToMain(t *testing.T) {
	mainRoot, wt := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{mainRoot: 1})
	t.Setenv("WRAP_COUNTS_FILE", f)
	sub := filepath.Join(wt, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out, err := runRow(t, sub)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 1 {
		t.Errorf("bh = %d, want 1 (main checkout fallback)", got)
	}
}

// TestRowPrintsRowExactlyAsStored: the row's bytes pass through untouched, so a
// field beadwatch's Row type does not know survives.
func TestRowPrintsRowExactlyAsStored(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	stored := `{"root":"` + mainRoot + `","bh":7,"x_unknown":[1, 2]}`
	if err := os.WriteFile(f, []byte(`{"`+mainRoot+`": `+stored+`}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, mainRoot)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if out != stored+"\n" {
		t.Errorf("out = %q, want %q", out, stored+"\n")
	}
}

// TestRowMatchesWriterKeysThroughSymlink: a dir reached through a symlink still
// finds the row the writer keyed by the resolved path.
func TestRowMatchesWriterKeysThroughSymlink(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(mainRoot, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{mainRoot: 3})
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, link)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 3 {
		t.Errorf("bh = %d, want 3", got)
	}
}

func TestRowNonGitDirPrintsNothing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{})
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, t.TempDir())
	if err != nil || out != "" {
		t.Errorf("RunRow = (%q, %v), want empty and nil", out, err)
	}
}

func TestRowAbsentRowPrintsNothing(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	writeFixture(t, f, map[string]int{"/elsewhere": 1})
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, mainRoot)
	if err != nil || out != "" {
		t.Errorf("RunRow = (%q, %v), want empty and nil", out, err)
	}
}

func TestRowCorruptFileIsError(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	f := filepath.Join(t.TempDir(), "counts.json")
	if err := os.WriteFile(f, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("WRAP_COUNTS_FILE", f)
	out, err := runRow(t, mainRoot)
	if err == nil || out != "" {
		t.Errorf("RunRow = (%q, %v), want empty and an error", out, err)
	}
}

func TestRowUnreadableFileIsError(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	t.Setenv("WRAP_COUNTS_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := runRow(t, mainRoot); err == nil {
		t.Error("RunRow on a missing file: want error")
	}
}

func TestRowUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := RunRow(args, &bytes.Buffer{}); err == nil {
			t.Errorf("RunRow(%v): want usage error", args)
		}
	}
}

// TestRowHonorsCacheDirAndWritesNothing: with no WRAP_COUNTS_FILE the reader uses
// BD_COUNTS_CACHE_DIR/counts.json, and leaves that dir exactly as it found it — no
// refresh lock, no mtime state, no rewrite (sd-3wp dumb reader).
func TestRowHonorsCacheDirAndWritesNothing(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	cache := t.TempDir()
	f := filepath.Join(cache, "counts.json")
	writeFixture(t, f, map[string]int{mainRoot: 4})
	before, err := os.Stat(f)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	t.Setenv("WRAP_COUNTS_FILE", "")
	t.Setenv("BD_COUNTS_CACHE_DIR", cache)
	out, err := runRow(t, mainRoot)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 4 {
		t.Errorf("bh = %d, want 4", got)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "counts.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache dir = %s, want only counts.json", strings.Join(names, ","))
	}
	after, err := os.Stat(f)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("counts.json was rewritten")
	}
}

func TestRowWrapCountsFileWins(t *testing.T) {
	mainRoot, _ := repoWithWorktree(t)
	cache := t.TempDir()
	writeFixture(t, filepath.Join(cache, "counts.json"), map[string]int{mainRoot: 5})
	wrap := filepath.Join(t.TempDir(), "wrap.json")
	writeFixture(t, wrap, map[string]int{mainRoot: 6})
	t.Setenv("BD_COUNTS_CACHE_DIR", cache)
	t.Setenv("WRAP_COUNTS_FILE", wrap)
	out, err := runRow(t, mainRoot)
	if err != nil {
		t.Fatalf("RunRow: %v", err)
	}
	if got := rowBH(t, out); got != 6 {
		t.Errorf("bh = %d, want 6 (WRAP_COUNTS_FILE)", got)
	}
}
