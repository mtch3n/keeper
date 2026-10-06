// Package audit is keeper's append-only record of what it intended to emit.
//
// One JSON object per line, opened O_APPEND, under ~/.config/keeper/audit.log.
// Record ids are UUIDv7, so the file sorts by time by its own content and not
// only by its order.
//
// # What it is not
//
// R10d, and the naming in this package follows it: the log records what keeper
// *intended* to emit — the policies it selected, the transform counts, the spans
// it redacted — and it does not verify the emitted bytes. §11.2's claim that
// recording emissions makes a redaction failure discoverable is bounded by
// exactly this: the log shows that a policy was selected, not that it was
// correctly applied. Verifying the latter is the job of the tests, not of the
// log. Nothing here should ever be read as proof that a value did not leave.
//
// # What must never reach it
//
//	R10a   a literal. Literals can be PII, and the log must not become the leak.
//	R10b   a result row. There is no field for one and Write adds none.
//	R10c   a comment, or a quoted identifier that matches nothing catalogued.
//
// [Normalize] is what makes the first and third true, and [Log.Write] re-applies
// it defensively rather than trusting its caller.
package audit

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Config constructs a [Log].
type Config struct {
	// Dir holds one file per UTC day. Its parent is where an older keeper kept
	// a plaintext audit.log, which this log never reads.
	Dir string
	// Key returns the 32-byte key every line is sealed under. keeperd derives
	// it from the vault's master key, so the log reads only where the vault
	// opens. Required: there is no plaintext mode.
	Key func() ([]byte, error)
	// Detector is the patterns pass [Log.ScreenIntent] runs. Required: R10c
	// screens the session intent, and without one there is no screen. It is
	// never a connection's chain: an intent belongs to a session.
	Detector ports.Detector
	// Now is the clock, for tests.
	Now func() time.Time
}

// Log is the append-only activity log: one file per UTC day, each line a
// record sealed with AES-256-GCM, so lines append without rewriting a file
// and a file read without the key yields nothing.
type Log struct {
	dir      string
	key      func() ([]byte, error)
	detector ports.Detector
	now      func() time.Time
	legacy   string

	mu         sync.Mutex
	aead       cipher.AEAD
	unreadable atomic.Int64
}

var _ ports.AuditLog = (*Log)(nil)

// New prepares the log directory. It opens no key yet: the vault opens after
// the daemon is wired, and the first write or read asks for the key.
func New(cfg Config) (*Log, error) {
	if cfg.Detector == nil {
		return nil, errors.New("audit: a detector is required to screen session intent (R10c)")
	}
	if cfg.Dir == "" || cfg.Key == nil {
		return nil, errors.New("audit: a directory and a key are required")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("audit: create log directory: %w", err)
	}
	l := &Log{dir: cfg.Dir, key: cfg.Key, detector: cfg.Detector, now: cfg.Now}
	if l.now == nil {
		l.now = time.Now
	}
	if p := filepath.Join(filepath.Dir(cfg.Dir), "audit.log"); fileExists(p) {
		l.legacy = p
	}
	return l, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LegacyPlaintext is the path of a plaintext audit.log an earlier keeper left,
// or empty. It is reported so the operator can remove it, and never read.
func (l *Log) LegacyPlaintext() string { return l.legacy }

// Unreadable is how many lines the most recent read could not open: a damaged
// or foreign line is skipped, counted and reported, never fatal.
func (l *Log) Unreadable() int { return int(l.unreadable.Load()) }

// Close releases nothing: every write opens and closes its day file.
func (l *Log) Close() error { return nil }

func (l *Log) cipher() (cipher.AEAD, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.aead != nil {
		return l.aead, nil
	}
	key, err := l.key()
	if err != nil {
		return nil, fmt.Errorf("audit: log key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("audit: log key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("audit: log key: %w", err)
	}
	l.aead = aead
	return aead, nil
}

func dayFile(dir string, t time.Time) string {
	return filepath.Join(dir, t.UTC().Format(dayLayout)+".log")
}

const dayLayout = "2006-01-02"

// Write appends one record. G9 never skips it.
//
// It fills in the record's id and timestamp when they are empty, so the caller
// can read the id back for the response's audit_id, and it re-normalizes the
// statement when it still carries something [Normalize] would have stripped.
// That second part is a backstop, not the mechanism: the pipeline is expected
// to pass normalized text with its own `known` callback, because this function
// has no catalog and must therefore drop every quoted identifier.
func (l *Log) Write(ctx context.Context, r *types.AuditRecord) error {
	if r == nil {
		return errors.New("audit: nil record")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = uuid.NewV7().String()
	}
	if r.At.IsZero() {
		r.At = l.now()
	}
	if hasStrippableLiteral(r.Statement) {
		r.Statement = Normalize(r.Statement, nil)
	}
	// R10c screens the intent at set_session_intent; screening it again here
	// means a record cannot carry PII because one call site forgot. A hit is
	// replaced by the kinds that matched, never by the text.
	if r.Intent != "" {
		if err := l.ScreenIntent(ctx, r.Intent); err != nil {
			kinds := "unscreened text"
			if ie, ok := errors.AsType[*IntentError](err); ok {
				kinds = strings.Join(ie.Types, ", ")
			}
			r.Intent = "\u27e8withheld: " + kinds + "\u27e9"
		}
	}

	line, err := json.Marshal(r, durationMarshaler)
	if err != nil {
		return fmt.Errorf("audit: encode record: %w", err)
	}
	// A record must be one line. json.Marshal escapes control characters inside
	// strings, so this is a guard against a future change rather than a live
	// case; a record that broke the invariant would silently corrupt the log.
	if i := strings.IndexAny(string(line), "\n\r"); i >= 0 {
		return errors.New("audit: encoded record contains a newline")
	}

	aead, err := l.cipher()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("audit: nonce: %w", err)
	}
	sealed := base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, line, nil))
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(dayFile(l.dir, r.At), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open log: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(sealed + "\n"); err != nil {
		return fmt.Errorf("audit: append record: %w", err)
	}
	return nil
}

// Query reads the log back for the Activity screen, most recent first. The log
// is in write order, so f.Before ends the read at that record: everything
// collected so far is older than it.
func (l *Log) Query(ctx context.Context, f ports.AuditFilter) ([]types.AuditRecord, error) {
	var out []types.AuditRecord
	err := l.each(ctx, func(r types.AuditRecord) bool {
		if f.Before != "" && r.ID == f.Before {
			return false
		}
		if matches(r, f) {
			out = append(out, r)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	slices.Reverse(out)
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// Get returns one record in full.
func (l *Log) Get(ctx context.Context, id string) (*types.AuditRecord, error) {
	var found *types.AuditRecord
	err := l.each(ctx, func(r types.AuditRecord) bool {
		if r.ID == id {
			found = &r
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("audit: no record %s", id)
	}
	return found, nil
}

func matches(r types.AuditRecord, f ports.AuditFilter) bool {
	switch {
	case f.SessionID != "" && r.SessionID != f.SessionID:
		return false
	case f.ConnectionID != "" && r.Connection != f.ConnectionID:
		return false
	case f.Tier != nil && r.Tier != *f.Tier:
		return false
	case !f.Since.IsZero() && r.At.Before(f.Since):
		return false
	}
	return true
}

// each streams every day file, oldest first, opening each line with the key.
// A line that will not open or parse is skipped and counted rather than
// failing the read: a truncated final line from a killed process, or one
// altered on disk, must not make the Activity screen unavailable.
func (l *Log) each(ctx context.Context, fn func(types.AuditRecord) bool) error {
	aead, err := l.cipher()
	if err != nil {
		return err
	}
	names, err := l.days()
	if err != nil {
		return err
	}
	bad := 0
	defer func() { l.unreadable.Store(int64(bad)) }()
	for _, name := range names {
		f, err := os.Open(filepath.Join(l.dir, name))
		if err != nil {
			return fmt.Errorf("audit: open log: %w", err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for sc.Scan() {
			if err := ctx.Err(); err != nil {
				f.Close()
				return err
			}
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			r, ok := open(aead, line)
			if !ok {
				bad++
				continue
			}
			if !fn(r) {
				f.Close()
				return nil
			}
		}
		f.Close()
	}
	return nil
}

func open(aead cipher.AEAD, line string) (types.AuditRecord, bool) {
	var r types.AuditRecord
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) < aead.NonceSize() {
		return r, false
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return r, false
	}
	return r, json.Unmarshal(plain, &r, durationUnmarshaler) == nil
}

// days lists the day files, oldest first.
func (l *Log) days() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: list log: %w", err)
	}
	var out []string
	for _, e := range entries {
		if _, ok := dayOf(e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

func dayOf(name string) (time.Time, bool) {
	base, ok := strings.CutSuffix(name, ".log")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(dayLayout, base)
	return t, err == nil
}

// Prune deletes every day file wholly older than the retention: a day's file
// goes once the whole of that day lies more than retentionDays in the past.
func (l *Log) Prune(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		return errors.New("audit: retention must be at least a day")
	}
	cutoff := l.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -retentionDays)
	names, err := l.days()
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		day, _ := dayOf(name)
		if day.AddDate(0, 0, 1).After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("audit: prune %s: %w", name, err)
		}
	}
	return nil
}

// IntentError is a rejected session intent. It names the kinds of identifier
// that were found and never the text: an error about PII that quotes the PII
// has moved the problem rather than solved it.
type IntentError struct {
	// Types are the detector labels that matched, deduplicated.
	Types []string
}

func (e *IntentError) Error() string {
	return "session intent carries " + strings.Join(e.Types, ", ") +
		"; describe the task without the value and pass it as a token parameter instead"
}

// ScreenIntent rejects a session intent that carries PII.
//
// R10c: stripping literals from the statement is not sufficient, because
// comments, quoted identifiers and the session intent are all attacker- or
// user-supplied text that can carry PII. The intent is screened by the same rule
// pass a `scan` column gets, and a hit is a rejection rather than a redaction —
// the intent is written once by a human or an agent, and asking for it again
// costs nothing, while a silently redacted intent would read as if it had been
// accepted as written.
//
// The daemon maps *[IntentError] to an agent-facing error; this package does not
// compose one, because CONTRACT §2 converts at the API boundary and nowhere else.
func (l *Log) ScreenIntent(ctx context.Context, intent string) error {
	if strings.TrimSpace(intent) == "" {
		return nil
	}
	res, err := l.detector.Detect(ctx, []string{intent})
	if err == nil && (len(res) == 0 || res[0] == nil) {
		err = errors.New("the detector did not examine the intent")
	}
	if err != nil {
		// A detector that failed screened nothing. Text that was not screened
		// is not text that is clean, and the intent is stored and shown to
		// humans on the approval screen, so it fails closed.
		return &IntentError{Types: []string{"unscreened text (the detector did not run)"}}
	}
	spans := res[0]
	if len(spans) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var kinds []string
	for _, s := range spans {
		if !seen[s.Type] {
			seen[s.Type] = true
			kinds = append(kinds, s.Type)
		}
	}
	slices.Sort(kinds)
	return &IntentError{Types: kinds}
}

// Normalize is [Normalize] as a method, satisfying ports.AuditLog.
func (l *Log) Normalize(sql string, known func(string) bool) string {
	return Normalize(sql, known)
}
