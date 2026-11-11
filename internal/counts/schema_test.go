package counts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkoosis/beadwatch/internal/bdcounts"
)

// wireBuckets decodes one counts.json row by its wire keys, independently of Row.
// It is the schema pin: the file's readers (the status line, strand's masthead)
// decode these names, so a rename in Row breaks these tests before it breaks them.
type wireBuckets struct {
	Waiting    int `json:"bh"`
	Open       int `json:"bo"`
	InProgress int `json:"bw"`
	Blocked    int `json:"bb"`
	Closed     int `json:"bcl"`
	Deferred   int `json:"bdf"`
}

// wireReader reads a counts.json the way an outside consumer does.
type wireReader struct{ path string }

func newWireReader(path string) wireReader { return wireReader{path: path} }

// Lookup returns the buckets for repoPath; ok is false for a missing file,
// malformed JSON, the reserved _meta key or an absent repo.
func (r wireReader) Lookup(repoPath string) (wireBuckets, bool) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return wireBuckets{}, false
	}
	var m map[string]wireBuckets
	if err := json.Unmarshal(data, &m); err != nil {
		return wireBuckets{}, false
	}
	key := filepath.Clean(repoPath)
	if key == bdcounts.MetaKey {
		return wireBuckets{}, false
	}
	b, ok := m[key]
	return b, ok
}

// Meta returns the liveness stamp; ok is false when the file or the stamp is absent.
func (r wireReader) Meta() (bdcounts.Meta, bool) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return bdcounts.Meta{}, false
	}
	var wrap struct {
		Meta bdcounts.Meta `json:"_meta"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil || wrap.Meta.LastRun == 0 {
		return bdcounts.Meta{}, false
	}
	return wrap.Meta, true
}

func writeWire(t *testing.T, body string) wireReader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counts.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write counts.json: %v", err)
	}
	return newWireReader(path)
}

// TestWireMapsBuckets pins the bh/bo/bw/bb/bcl/bdf names against a row shaped as
// the writer emits it (extra keys included).
func TestWireMapsBuckets(t *testing.T) {
	r := writeWire(t, `{"/repo/a":{"root":"/repo/a","prefix":"a","bh":1,"bo":2,"bw":3,"bb":4,"bcl":5,"bdf":6,"ts":99}}`)
	got, ok := r.Lookup("/repo/a")
	want := wireBuckets{Waiting: 1, Open: 2, InProgress: 3, Blocked: 4, Closed: 5, Deferred: 6}
	if !ok || got != want {
		t.Errorf("Lookup = %+v ok=%v, want %+v", got, ok, want)
	}
}

// TestWireWriterEmitsPinnedKeys marshals a Row and reads it back by wire key, so
// the writer's own output is held to the names above.
func TestWireWriterEmitsPinnedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	row := Row{Root: "/repo/a", BH: 1, BO: 2, BW: 3, BB: 4, BCl: 5, BDf: 6}
	if err := writeRowsAtomic(path, map[string]Row{"/repo/a": row}, bdcounts.Meta{LastRun: 7, Version: "v"}); err != nil {
		t.Fatalf("writeRowsAtomic: %v", err)
	}
	r := newWireReader(path)
	got, ok := r.Lookup("/repo/a/")
	want := wireBuckets{Waiting: 1, Open: 2, InProgress: 3, Blocked: 4, Closed: 5, Deferred: 6}
	if !ok || got != want {
		t.Errorf("Lookup = %+v ok=%v, want %+v", got, ok, want)
	}
	if m, ok := r.Meta(); !ok || m.LastRun != 7 || m.Version != "v" {
		t.Errorf("Meta = %+v ok=%v, want lastRun 7 version v", m, ok)
	}
}

// TestWireMisses: every not-ok path a consumer must survive.
func TestWireMisses(t *testing.T) {
	if _, ok := writeWire(t, `{"/repo/a":{"bh":1}}`).Lookup("/repo/b"); ok {
		t.Error("unlisted repo = ok, want miss")
	}
	if _, ok := newWireReader(filepath.Join(t.TempDir(), "nope.json")).Lookup("/repo/a"); ok {
		t.Error("missing file = ok, want miss")
	}
	if _, ok := writeWire(t, `{not json`).Lookup("/repo/a"); ok {
		t.Error("malformed json = ok, want miss")
	}
}

// TestWireMetaCoexists: the reserved _meta key never reads as a repo and does not
// perturb a real row's decode; an unstamped file has no Meta.
func TestWireMetaCoexists(t *testing.T) {
	r := writeWire(t, `{"_meta":{"lastRun":1710000000,"version":"1.2.3"},"/repo/a":{"bh":1,"bo":2}}`)
	if _, ok := r.Lookup("_meta"); ok {
		t.Error("Lookup(_meta) = ok, want miss")
	}
	if got, ok := r.Lookup("/repo/a"); !ok || got.Waiting != 1 || got.Open != 2 {
		t.Errorf("Lookup(/repo/a) = %+v ok=%v, want Waiting 1 Open 2", got, ok)
	}
	if m, ok := r.Meta(); !ok || m.LastRun != 1710000000 || m.Version != "1.2.3" {
		t.Errorf("Meta = %+v ok=%v", m, ok)
	}
	if _, ok := writeWire(t, `{"/repo/a":{"bh":1}}`).Meta(); ok {
		t.Error("unstamped file Meta = ok, want miss")
	}
}

// TestReadRowsKeepsRowsOnMistypedField: one skewed row (a string where a number
// belongs) must not drop the other repos' last-good rows; only malformed JSON
// starts an empty base.
func TestReadRowsKeepsRowsOnMistypedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	body := `{"/r/good":{"root":"/r/good","bo":3},"/r/bad":{"root":"/r/bad","bh":"x"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if rows := readRows(path); rows["/r/good"].BO != 3 {
		t.Errorf("a mistyped field in /r/bad dropped /r/good: %+v", rows)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rows := readRows(path); len(rows) != 0 {
		t.Errorf("malformed json: rows = %+v, want empty", rows)
	}
}
