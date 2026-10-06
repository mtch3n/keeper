package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func newLog(t *testing.T) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keeper", "activity")
	l, err := New(Config{Key: testKey, Dir: path, Detector: detect.NewPatterns()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestWriteAppendsOneLinePerRecord(t *testing.T) {
	l := newLog(t)
	for i := range 3 {
		r := &types.AuditRecord{
			SessionID:  "s1",
			Connection: "c1",
			Statement:  "SELECT id FROM users WHERE email = $1",
			Tier:       types.Tier(i),
			RowCount:   i,
			Duration:   time.Duration(i) * time.Millisecond,
		}
		if err := l.Write(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		if r.ID == "" || r.At.IsZero() {
			t.Fatalf("Write did not fill in id and timestamp: %+v", r)
		}
	}
	names, err := l.days()
	if err != nil || len(names) != 1 {
		t.Fatalf("day files = %v, %v", names, err)
	}
	file := filepath.Join(l.dir, names[0])
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "{") {
			t.Errorf("line is plaintext JSON: %q", line)
		}
	}
	// Ids are UUIDv7, so the log sorts by time by its own content.
	var prev string
	if err := l.each(t.Context(), func(r types.AuditRecord) bool {
		if prev != "" && r.ID <= prev {
			t.Errorf("ids do not sort ascending: %s then %s", prev, r.ID)
		}
		prev = r.ID
		return true
	}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode is %v, want 0600", fi.Mode().Perm())
	}
}

func TestWriteReNormalizesDefensively(t *testing.T) {
	// The pipeline is expected to normalize, with its own catalog callback.
	// Write does not trust it: R10a says the log never receives a literal.
	l := newLog(t)
	r := &types.AuditRecord{
		Statement: "SELECT id /* patient: Jane Doe */ FROM users WHERE email = 'jane@example.com'",
	}
	if err := l.Write(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Statement, "Jane Doe") || strings.Contains(r.Statement, "jane@example.com") {
		t.Fatalf("statement still carries PII: %q", r.Statement)
	}
	stored, err := l.Get(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"Jane Doe", "jane@example.com", "patient"} {
		if strings.Contains(stored.Statement, needle) {
			t.Errorf("stored record contains %q", needle)
		}
	}
	// Already-normalized text, with a catalogued identifier, survives untouched.
	r2 := &types.AuditRecord{Statement: `SELECT "Email" FROM "order" WHERE id = $1`}
	if err := l.Write(t.Context(), r2); err != nil {
		t.Fatal(err)
	}
	if r2.Statement != `SELECT "Email" FROM "order" WHERE id = $1` {
		t.Errorf("a normalized statement was rewritten: %q", r2.Statement)
	}
}

func TestWriteWithholdsAnUnscreenedIntent(t *testing.T) {
	l := newLog(t)
	r := &types.AuditRecord{
		Intent:    "look up the order for jane@example.com",
		Statement: "SELECT id FROM orders WHERE email = $1",
	}
	if err := l.Write(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Intent, "jane@example.com") {
		t.Fatalf("intent kept the value: %q", r.Intent)
	}
	if !strings.Contains(r.Intent, "email") {
		t.Errorf("intent should name what was found: %q", r.Intent)
	}
	clean := &types.AuditRecord{Intent: "reconcile last week's refunds", Statement: "SELECT 1"}
	if err := l.Write(t.Context(), clean); err != nil {
		t.Fatal(err)
	}
	if clean.Intent != "reconcile last week's refunds" {
		t.Errorf("a clean intent was altered: %q", clean.Intent)
	}
}

func TestQueryAndGet(t *testing.T) {
	l := newLog(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tiers := []types.Tier{types.Tier0Run, types.Tier1Record, types.Tier3Approve}
	var ids []string
	for i, tier := range tiers {
		r := &types.AuditRecord{
			At:         base.Add(time.Duration(i) * time.Hour),
			SessionID:  []string{"s1", "s2", "s1"}[i],
			Connection: []string{"c1", "c1", "c2"}[i],
			Tier:       tier,
			Statement:  "SELECT 1",
			Transforms: map[string]types.Transform{"email": {Policy: types.PolicyToken, Namespace: "email", Basis: types.BasisCatalog}},
			Duration:   250 * time.Millisecond,
		}
		if err := l.Write(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}

	all, err := l.Query(t.Context(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d records, want 3", len(all))
	}
	if all[0].ID != ids[2] {
		t.Errorf("Query is not most-recent-first: %v", all[0].ID)
	}

	bySession, _ := l.Query(t.Context(), ports.AuditFilter{SessionID: "s1"})
	if len(bySession) != 2 {
		t.Errorf("session filter returned %d", len(bySession))
	}
	byConn, _ := l.Query(t.Context(), ports.AuditFilter{ConnectionID: "c2"})
	if len(byConn) != 1 {
		t.Errorf("connection filter returned %d", len(byConn))
	}
	tier := types.Tier1Record
	byTier, _ := l.Query(t.Context(), ports.AuditFilter{Tier: &tier})
	if len(byTier) != 1 || byTier[0].Tier != types.Tier1Record {
		t.Errorf("tier filter returned %v", byTier)
	}
	since, _ := l.Query(t.Context(), ports.AuditFilter{Since: base.Add(90 * time.Minute)})
	if len(since) != 1 {
		t.Errorf("since filter returned %d", len(since))
	}
	limited, _ := l.Query(t.Context(), ports.AuditFilter{Limit: 2})
	if len(limited) != 2 || limited[0].ID != ids[2] {
		t.Errorf("limit returned %v", limited)
	}
	// The next page starts after the last record of this one.
	older, _ := l.Query(t.Context(), ports.AuditFilter{Limit: 2, Before: limited[1].ID})
	if len(older) != 1 || older[0].ID != ids[0] {
		t.Errorf("before returned %v", older)
	}

	got, err := l.Get(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s2" || got.Duration != 250*time.Millisecond {
		t.Errorf("Get round-trip lost fields: %+v", got)
	}
	if got.Transforms["email"].Namespace != "email" {
		t.Errorf("transforms did not round-trip: %+v", got.Transforms)
	}
	if _, err := l.Get(t.Context(), "nope"); err == nil {
		t.Error("Get should fail for an unknown id")
	}
}

func TestQueryToleratesATruncatedLine(t *testing.T) {
	l := newLog(t)
	if err := l.Write(t.Context(), &types.AuditRecord{Statement: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	names, _ := l.days()
	f, err := os.OpenFile(filepath.Join(l.dir, names[0]), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"broken`)
	f.Close()
	got, err := l.Query(t.Context(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d records, want 1", len(got))
	}
}

func TestQueryOnAMissingFile(t *testing.T) {
	l := newLog(t)
	os.RemoveAll(l.dir)
	got, err := l.Query(t.Context(), ports.AuditFilter{})
	if err != nil || len(got) != 0 {
		t.Errorf("Query on a missing log = %v, %v", got, err)
	}
}

func TestScreenIntent(t *testing.T) {
	l := newLog(t)
	rejected := []string{
		"find the order for jane@example.com",
		"check card 4111 1111 1111 1111",
		"look up 078-05-1120",
		"the account GB82WEST12345698765432",
	}
	for _, intent := range rejected {
		err := l.ScreenIntent(t.Context(), intent)
		if err == nil {
			t.Errorf("ScreenIntent(%q) accepted it", intent)
			continue
		}
		ie, ok := errors.AsType[*IntentError](err)
		if !ok {
			t.Errorf("ScreenIntent(%q) returned %T", intent, err)
			continue
		}
		if len(ie.Types) == 0 {
			t.Errorf("ScreenIntent(%q) named no types", intent)
		}
		// The error names kinds, never the text.
		for _, frag := range strings.Fields(intent) {
			if len(frag) > 6 && strings.Contains(err.Error(), frag) {
				t.Errorf("ScreenIntent error echoed the intent: %q", err.Error())
			}
		}
	}
	accepted := []string{
		"", "   ",
		"reconcile last week's refunds by region",
		"find orders placed after the price change",
	}
	for _, intent := range accepted {
		if err := l.ScreenIntent(t.Context(), intent); err != nil {
			t.Errorf("ScreenIntent(%q) = %v", intent, err)
		}
	}
}

func TestScreenIntentFailsClosedOnADetectorError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	l, err := New(Config{Key: testKey, Dir: path, Detector: brokenDetector{}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.ScreenIntent(t.Context(), "anything at all"); err == nil {
		t.Error("an unscreened intent was accepted")
	}
}

type brokenDetector struct{}

func (brokenDetector) Detect(context.Context, []string) ([][]ports.Span, error) {
	return nil, errors.New("sidecar unreachable")
}
func (brokenDetector) Identity() ports.DetectorIdentity {
	return ports.DetectorIdentity{Name: "broken", NetworkPosture: "unverified"}
}

func TestConcurrentWrites(t *testing.T) {
	l := newLog(t)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			if err := l.Write(t.Context(), &types.AuditRecord{
				SessionID: "s", Statement: "SELECT $1", RowCount: i,
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := l.Query(t.Context(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 32 {
		t.Errorf("got %d records, want 32", len(got))
	}
}

func TestNewRequiresADetector(t *testing.T) {
	if _, err := New(Config{Key: testKey, Dir: filepath.Join(t.TempDir(), "activity")}); err == nil {
		t.Error("New accepted a nil detector")
	}
}

// An intent belongs to a session, not a connection, so whatever passes a
// connection runs, the intent is screened in process by the patterns pass and
// never leaves the machine.
func Test_DET_C18_AnIntentIsScreenedByThePatternsPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	l, err := New(Config{Key: testKey, Dir: path, Detector: detect.NewPatterns()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	err = l.ScreenIntent(t.Context(), "follow up with jane@example.com")
	ie, ok := errors.AsType[*IntentError](err)
	if !ok || !slices.Contains(ie.Types, "email_address") {
		t.Fatalf("ScreenIntent = %v, want an email_address rejection", err)
	}
}

func Test_DET_C19_AnUnscreenedIntentIsRejectedAndNotRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	l, err := New(Config{Key: testKey, Dir: path, Detector: brokenDetector{}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	const intent = "reconcile refunds for OPS-441"
	if _, ok := errors.AsType[*IntentError](l.ScreenIntent(t.Context(), intent)); !ok {
		t.Fatal("an intent the detector did not screen was not rejected as one to restate")
	}
	// The audit record itself is never dropped; its intent text is withheld.
	if err := l.Write(t.Context(), &types.AuditRecord{ID: "a1", Intent: intent}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "OPS-441") {
		t.Errorf("the unscreened intent reached the log: %s", data)
	}
}
