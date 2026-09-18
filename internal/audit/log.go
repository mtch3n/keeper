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
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Config constructs a [Log].
type Config struct {
	// Path is the log file. Empty uses [DefaultPath].
	Path string
	// Detector is the rules pass [Log.ScreenIntent] runs. Required: R10c
	// screens the session intent with the same pass a scan column gets, and
	// without one there is no screen.
	Detector ports.Detector
	// Now is the clock, for tests.
	Now func() time.Time
}

// Log is the append-only JSONL audit log.
type Log struct {
	path     string
	detector ports.Detector
	now      func() time.Time

	mu sync.Mutex
	f  *os.File
}

var _ ports.AuditLog = (*Log)(nil)

// DefaultPath is ~/.config/keeper/audit.log, honouring XDG_CONFIG_HOME.
//
// SPEC R5.2a: ~/.config/keeper holds only the vault, the daemon's own state and
// this log. The catalog lives in the project repo, because it is reviewed; this
// is not.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("audit: locate config directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "keeper", "audit.log"), nil
}

// New opens the log, creating the directory and file if needed.
func New(cfg Config) (*Log, error) {
	if cfg.Detector == nil {
		return nil, errors.New("audit: a detector is required to screen session intent (R10c)")
	}
	path := cfg.Path
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit: create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open log: %w", err)
	}
	l := &Log{path: path, detector: cfg.Detector, now: cfg.Now, f: f}
	if l.now == nil {
		l.now = time.Now
	}
	return l, nil
}

// Path reports where the log is, for doctor.
func (l *Log) Path() string { return l.path }

// Close releases the file handle.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

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

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("audit: log is closed")
	}
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit: append record: %w", err)
	}
	return nil
}

// Query reads the log back for the Activity screen, most recent first.
func (l *Log) Query(ctx context.Context, f ports.AuditFilter) ([]types.AuditRecord, error) {
	var out []types.AuditRecord
	err := l.each(ctx, func(r types.AuditRecord) bool {
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

// each streams the log. A line that will not parse is skipped rather than
// failing the read: a truncated final line from a killed process must not make
// the Activity screen unavailable.
func (l *Log) each(ctx context.Context, fn func(types.AuditRecord) bool) error {
	f, err := os.Open(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("audit: open log: %w", err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				var r types.AuditRecord
				if json.Unmarshal([]byte(trimmed), &r, durationUnmarshaler) == nil {
					if !fn(r) {
						return nil
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("audit: read log: %w", err)
		}
	}
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
	spans, err := l.detector.Scan(ctx, intent)
	if err != nil {
		// A detector that failed screened nothing. Text that was not screened
		// is not text that is clean, and the intent is stored and shown to
		// humans on the approval screen, so it fails closed.
		return &IntentError{Types: []string{"unscreened text (the detector did not run)"}}
	}
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
