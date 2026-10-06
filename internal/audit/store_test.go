package audit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func testKey() ([]byte, error) { return bytes.Repeat([]byte{7}, 32), nil }

func newEncryptedLog(t *testing.T, now time.Time) (*Log, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "activity")
	l, err := New(Config{Dir: dir, Key: testKey, Detector: detect.NewPatterns(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, dir
}

func dayFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

var today = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func Test_LOG_C1_ARecordReadsBackWithTheKey(t *testing.T) {
	l, _ := newEncryptedLog(t, today)
	if err := l.Write(context.Background(), &types.AuditRecord{ID: "a1", Intent: "reconcile refunds", Statement: "SELECT id FROM orders", Connection: "c1"}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get(context.Background(), "a1")
	if err != nil || got.Intent != "reconcile refunds" || got.Statement != "SELECT id FROM orders" || got.Connection != "c1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func Test_LOG_C2_TheFileHoldsNoReadableText(t *testing.T) {
	l, dir := newEncryptedLog(t, today)
	if err := l.Write(context.Background(), &types.AuditRecord{ID: "a1", Intent: "reconcile refunds", Statement: "SELECT id FROM orders"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range dayFiles(t, dir) {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		for _, s := range []string{"reconcile refunds", "SELECT id FROM orders", "orders"} {
			if bytes.Contains(raw, []byte(s)) {
				t.Errorf("%s holds %q in the clear", name, s)
			}
		}
	}
}

func Test_LOG_C3_ADamagedLineIsReportedAndTheRestStillRead(t *testing.T) {
	l, dir := newEncryptedLog(t, today)
	for _, id := range []string{"a1", "a2", "a3"} {
		if err := l.Write(context.Background(), &types.AuditRecord{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, dayFiles(t, dir)[0])
	raw, _ := os.ReadFile(file)
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	lines[1][len(lines[1])/2] ^= 0x01
	if err := os.WriteFile(file, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := l.Query(context.Background(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("read %d records, want the 2 undamaged ones", len(got))
	}
	if n := l.Unreadable(); n != 1 {
		t.Errorf("unreadable = %d, want 1", n)
	}
}

func writeOn(t *testing.T, l *Log, id string, at time.Time) {
	t.Helper()
	if err := l.Write(context.Background(), &types.AuditRecord{ID: id, At: at}); err != nil {
		t.Fatal(err)
	}
}

func Test_LOG_C4_FilesOlderThanTheRetentionArePruned(t *testing.T) {
	l, dir := newEncryptedLog(t, today)
	writeOn(t, l, "old", today.AddDate(0, 0, -40))
	writeOn(t, l, "recent", today.AddDate(0, 0, -10))
	if err := l.Prune(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	files := dayFiles(t, dir)
	if len(files) != 1 || files[0] != today.AddDate(0, 0, -10).Format("2006-01-02")+".log" {
		t.Errorf("files after pruning = %v, want only the 10-day-old one", files)
	}
}

func Test_LOG_C5_ShorterRetentionPrunesOnTheNextRun(t *testing.T) {
	l, dir := newEncryptedLog(t, today)
	writeOn(t, l, "recent", today.AddDate(0, 0, -10))
	writeOn(t, l, "fresh", today.AddDate(0, 0, -2))
	if err := l.Prune(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	files := dayFiles(t, dir)
	if len(files) != 1 || files[0] != today.AddDate(0, 0, -2).Format("2006-01-02")+".log" {
		t.Errorf("files after shortening = %v, want only the 2-day-old one", files)
	}
}

func Test_LOG_C6_APlaintextLogIsReportedAndNeverRead(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "audit.log")
	if err := os.WriteFile(legacy, []byte(`{"id":"plain","intent":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := New(Config{Dir: filepath.Join(root, "activity"), Key: testKey, Detector: detect.NewPatterns(), Now: func() time.Time { return today }})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.LegacyPlaintext(); got != legacy {
		t.Errorf("LegacyPlaintext = %q, want %q", got, legacy)
	}
	recs, _ := l.Query(context.Background(), ports.AuditFilter{})
	for _, r := range recs {
		if r.ID == "plain" {
			t.Error("the plaintext log was read")
		}
	}
}
