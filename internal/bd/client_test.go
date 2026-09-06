package bd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBD writes an executable shell script standing in for the bd binary and
// returns a Client pointed at it plus the path of the file it logs args to.
// body is the script's case body (it may emit JSON on stdout); every call also
// appends its full arg list as one line to the log.
func fakeBD(t *testing.T, body string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + log + "'\n" +
		body + "\n"
	path := filepath.Join(dir, "bd")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	return &Client{Bin: path}, log
}

func readLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestListPassesLimitZero pins List's own flag vocabulary: it always lifts
// bd's default row cap.
func TestListPassesLimitZero(t *testing.T) {
	c, log := fakeBD(t, `echo '[]'`)
	if _, err := c.List(context.Background(), ListOpts{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	got := readLog(t, log)
	want := "list --json --limit 0"
	if got[0] != want {
		t.Errorf("args = %q, want %q", got[0], want)
	}
}

// TestRun_ReturnsContextErr_When_CancelledWaitingOnHeldLock pins the liveness
// fix from strand's st-kl8: the process-wide single-writer lock (execMu) must
// honor ctx while a caller waits for it. We hold the lock for the whole test
// so the caller can never acquire it; with a context-blind Lock() the caller
// blocks behind the holder indefinitely (the bug). A ctx-aware acquisition
// returns ctx.Err() promptly even though the lock is still held — proving the
// wait, not just the run, respects ctx.
func TestRun_ReturnsContextErr_When_CancelledWaitingOnHeldLock(t *testing.T) {
	execMu <- struct{}{}
	defer func() { <-execMu }()

	c := &Client{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := c.run(ctx, "list", "--json")
		errCh <- err
	}()

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run blocked on the held lock past ctx cancel; execMu must honor ctx")
	}
}

// TestRun_ReturnsContextErr_When_AlreadyCancelled pins the fast-fail path:
// with the lock free, an already-cancelled ctx must still return ctx.Err()
// and never grab the lock.
func TestRun_ReturnsContextErr_When_AlreadyCancelled(t *testing.T) {
	c := &Client{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // done before run; lock is free.

	_, err := c.run(ctx, "list", "--json")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run returned %v, want context.Canceled", err)
	}
	select {
	case execMu <- struct{}{}:
		<-execMu
	default:
		t.Fatal("run acquired the lock despite a cancelled ctx")
	}
}

// TestDecodeIssuePriority pins the Priority decode contract: Issue.Priority is
// *int. A present field decodes to a non-nil pointer (0 included); an absent
// field decodes to nil — no false P0.
func TestDecodeIssuePriority(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{name: "present zero is P0", json: `[{"id":"a-1","priority":0}]`, want: new(int)},
		{name: "present nonzero round-trips", json: `[{"id":"a-2","priority":2}]`, want: new(2)},
		{name: "present highest boundary", json: `[{"id":"a-3","priority":4}]`, want: new(4)},
		{name: "absent decodes to nil (no false P0)", json: `[{"id":"a-4"}]`, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := decodeIssues([]byte(tc.json))
			if err != nil {
				t.Fatalf("decodeIssues: %v", err)
			}
			if len(issues) != 1 {
				t.Fatalf("got %d issues, want 1", len(issues))
			}
			got := issues[0].Priority
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("Priority = %d, want nil", *got)
			case tc.want != nil && got == nil:
				t.Errorf("Priority = nil, want %d", *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Errorf("Priority = %d, want %d", *got, *tc.want)
			}
		})
	}
}

// TestDecodeStatsWireShape pins the `summary` wrapper key that Stats() depends
// on. bd stats --json emits { "summary": { "open_issues": N, … } }; if that
// key is ever renamed or removed, json.Unmarshal silently returns an
// all-zero Stats with no error.
func TestDecodeStatsWireShape(t *testing.T) {
	const realisticPayload = `{
		"summary": {
			"open_issues": 5,
			"in_progress_issues": 2,
			"blocked_issues": 1,
			"closed_issues": 10,
			"deferred_issues": 3,
			"total_issues": 21
		}
	}`

	t.Run("correct summary key decodes non-zero Stats", func(t *testing.T) {
		var resp struct {
			Summary Stats `json:"summary"`
		}
		if err := json.Unmarshal([]byte(realisticPayload), &resp); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		s := resp.Summary
		if s.Open != 5 || s.InProgress != 2 || s.Blocked != 1 || s.Closed != 10 || s.Deferred != 3 || s.Total != 21 {
			t.Errorf("Stats fields mismatch: got %+v; check Stats json tags match the bd wire format", s)
		}
	})

	t.Run("wrong wrapper key silently yields zero Stats (silent-degradation pinned)", func(t *testing.T) {
		wrongKey := `{"data":{"open_issues":5,"in_progress_issues":2,"total_issues":7}}`
		var resp struct {
			Summary Stats `json:"summary"`
		}
		if err := json.Unmarshal([]byte(wrongKey), &resp); err != nil {
			t.Fatalf("unexpected Unmarshal error with wrong key: %v", err)
		}
		s := resp.Summary
		if s.Open != 0 || s.Total != 0 {
			t.Errorf("expected all-zero Stats on missing 'summary' key, got %+v", s)
		}
	})

	t.Run("Stats json field tags round-trip correctly", func(t *testing.T) {
		original := Stats{Open: 3, InProgress: 1, Blocked: 0, Closed: 7, Deferred: 2, Total: 13}
		payload, err := json.Marshal(struct {
			Summary Stats `json:"summary"`
		}{Summary: original})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var got struct {
			Summary Stats `json:"summary"`
		}
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("json.Unmarshal round-trip: %v", err)
		}
		if got.Summary != original {
			t.Errorf("round-trip mismatch: got %+v, want %+v", got.Summary, original)
		}
	})
}

// TestDecodeEpicStatusWireShape pins the `bd epic status --json` contract the
// epic derivation reads: an array of {epic:{id,status}, total_children,
// closed_children} rows.
func TestDecodeEpicStatusWireShape(t *testing.T) {
	const payload = `[
		{"epic":{"id":"st-2fy","status":"open"},"total_children":6,"closed_children":2},
		{"epic":{"id":"st-alg","status":"closed"},"total_children":4,"closed_children":4}
	]`
	var rows []EpicStatus
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(rows))
	}
	if rows[0].Epic.ID != "st-2fy" || rows[0].Epic.Status != StatusOpen {
		t.Errorf("row0 epic mismatch: got %+v", rows[0].Epic)
	}
	if rows[0].TotalChildren != 6 || rows[0].ClosedChildren != 2 {
		t.Errorf("row0 children mismatch: got total=%d closed=%d", rows[0].TotalChildren, rows[0].ClosedChildren)
	}
	if rows[1].Epic.Status != StatusClosed {
		t.Errorf("row1 status mismatch: got %q", rows[1].Epic.Status)
	}
}

// TestRankPresentZero guards the present-zero case: a metadata rank of exactly
// 0 must return (0, true), not (0, false).
func TestRankPresentZero(t *testing.T) {
	cases := []struct {
		name    string
		issue   Issue
		wantVal float64
		wantOk  bool
	}{
		{name: "present zero returns (0, true)", issue: Issue{Metadata: map[string]any{"rank": float64(0)}}, wantVal: 0, wantOk: true},
		{name: "absent rank key returns (0, false)", issue: Issue{}, wantVal: 0, wantOk: false},
		{name: "nil metadata returns (0, false)", issue: Issue{Metadata: nil}, wantVal: 0, wantOk: false},
		{name: "present positive float returns correct value", issue: Issue{Metadata: map[string]any{"rank": float64(3.5)}}, wantVal: 3.5, wantOk: true},
		{name: "present negative float returns correct value", issue: Issue{Metadata: map[string]any{"rank": float64(-1.0)}}, wantVal: -1.0, wantOk: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotVal, gotOk := tc.issue.Rank()
			if gotOk != tc.wantOk {
				t.Errorf("Rank() ok = %v, want %v", gotOk, tc.wantOk)
			}
			if gotVal != tc.wantVal {
				t.Errorf("Rank() val = %v, want %v", gotVal, tc.wantVal)
			}
		})
	}
}

// TestDecodeIssueAbsentPriorityIsIndistinguishable: with a plain int,
// "priority":0 and an absent field would decode to the SAME value. With
// Priority as *int the two are distinct — present-0 is a non-nil pointer to
// 0, absent is nil.
func TestDecodeIssueAbsentPriorityIsIndistinguishable(t *testing.T) {
	withZero, err := decodeIssues([]byte(`[{"id":"x","priority":0}]`))
	if err != nil {
		t.Fatalf("decode present-zero: %v", err)
	}
	absent, err := decodeIssues([]byte(`[{"id":"x"}]`))
	if err != nil {
		t.Fatalf("decode absent: %v", err)
	}
	if len(withZero) != 1 || len(absent) != 1 {
		t.Fatalf("expected exactly 1 issue in each result, got len(withZero)=%d, len(absent)=%d", len(withZero), len(absent))
	}
	if withZero[0].Priority == nil {
		t.Fatal("present-0 decoded to nil; expected a non-nil pointer to 0")
	}
	if *withZero[0].Priority != 0 {
		t.Fatalf("present-0 = %d, want 0", *withZero[0].Priority)
	}
	if absent[0].Priority != nil {
		t.Fatalf("absent = %d, want nil — the int collapse must be closed", *absent[0].Priority)
	}
}
