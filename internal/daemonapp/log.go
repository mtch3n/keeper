package daemonapp

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
)

// sensitiveKeys are attribute names that must never reach a log line. SQL is
// here because a statement carries literals (R10a), value and passphrase because
// they are the secrets the whole product exists to keep out of transcripts,
// token because a token is a capability into a session's reverse map, request_id
// because it is the capability the local page is protected by, and dsn because
// it holds a password.
//
// The pipeline and the api already avoid passing any of them. This handler is
// the second line: a log statement written next year cannot leak one by accident.
var sensitiveKeys = []string{
	"sql", "statement", "query", "params", "param", "literal", "value", "values",
	"row", "rows", "token", "tokens", "request_id", "requestid", "passphrase",
	"password", "dsn", "secret", "body", "intent", "purpose",
}

// redactingHandler drops the value of any attribute whose name looks sensitive,
// at any nesting depth.
type redactingHandler struct{ slog.Handler }

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a))
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, redactAttr(a))
	}
	return redactingHandler{h.Handler.WithAttrs(out)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{h.Handler.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if slices.Contains(sensitiveKeys, strings.ToLower(a.Key)) {
		return slog.String(a.Key, "[redacted]")
	}
	if a.Value.Kind() == slog.KindGroup {
		sub := a.Value.Group()
		out := make([]any, 0, len(sub))
		for _, s := range sub {
			out = append(out, redactAttr(s))
		}
		return slog.Group(a.Key, out...)
	}
	return a
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(redactingHandler{h})
}
