package redact

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// maxJSONDepth bounds how far applyPaths descends. A document deeper than this
// is not classified; the column policy handles it.
const maxJSONDepth = 64

// applyPaths applies §5.7 key-path policies to a json or jsonb value.
//
// A jsonb column is a document, not a value: (tableOID, attnum) identifies the
// column and says nothing about its contents, and rows may differ in shape. The
// catalog extends the same identity idea one level down, keyed by JSONPath —
// "$.customer.email" for an object member, "$.items[*].email" for an array
// element, index-independent because the index is not an identity.
//
// R5.7: a path that is not catalogued is treated as an unknown column and
// redacts, and the statement still runs. That rule only applies once *some*
// path has been catalogued; until then the whole column is `scan`, which is the
// caller's business and not this function's.
//
// The document is rewritten through the token stream rather than decoded into
// map[string]any and re-encoded, so member order, number formatting and string
// escaping all survive untouched. A masked document that also silently
// reordered its keys would be much harder to read a diff of.
//
// ok is false when the value is not a JSON document, in which case the caller
// falls back to the column's own policy.
func (r *Redactor) applyPaths(ctx context.Context, sess *session, pl *columnPlan, v any, now time.Time) (any, bool) {
	var src []byte
	decoded := false
	switch t := v.(type) {
	case string:
		src = []byte(t)
	case []byte:
		src = t
	case jsontext.Value:
		src = t
	case map[string]any, []any:
		// The driver decodes json and jsonb into these.
		encoded, err := json.Marshal(t)
		if err != nil {
			return RedactedMarker, true
		}
		src, decoded = encoded, true
	default:
		// A number or boolean document: paths describe objects and arrays only.
		return RedactedMarker, true
	}
	if !jsontext.Value(src).IsValid() {
		// The driver hands a jsonb string scalar over as the bare string.
		return RedactedMarker, true
	}
	var buf bytes.Buffer
	dec := jsontext.NewDecoder(bytes.NewReader(src))
	enc := jsontext.NewEncoder(&buf)
	if err := r.walkJSON(ctx, sess, pl, dec, enc, "$", 0, now); err != nil {
		// A document keeper could not walk is a document keeper did not
		// classify. It is masked whole rather than emitted.
		return RedactedMarker, true
	}
	out := strings.TrimRight(buf.String(), "\n")
	if decoded {
		var masked any
		if err := json.Unmarshal([]byte(out), &masked); err != nil {
			return RedactedMarker, true
		}
		return masked, true
	}
	if _, isString := v.(string); isString {
		return out, true
	}
	return []byte(out), true
}

func (r *Redactor) walkJSON(ctx context.Context, sess *session, pl *columnPlan, dec *jsontext.Decoder, enc *jsontext.Encoder, path string, depth int, now time.Time) error {
	if depth > maxJSONDepth {
		return errors.New("redact: json nesting too deep to classify")
	}
	switch dec.PeekKind() {
	case jsontext.KindBeginObject:
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		hidden := 0
		for dec.PeekKind() != jsontext.KindEndObject {
			name, err := dec.ReadToken()
			if err != nil {
				return err
			}
			child := path + "." + name.String()
			shown := name.String()
			if !pathLeadsToDeclared(pl.paths, child) {
				// The key is data too. Hidden keys need distinct names for the
				// object to stay valid.
				hidden++
				shown = RedactedMarker
				if hidden > 1 {
					shown = RedactedSpan(strconv.Itoa(hidden))
				}
			}
			if err := enc.WriteToken(jsontext.String(shown)); err != nil {
				return err
			}
			if err := r.walkJSON(ctx, sess, pl, dec, enc, child, depth+1, now); err != nil {
				return err
			}
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)

	case jsontext.KindBeginArray:
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		child := path + "[*]"
		for dec.PeekKind() != jsontext.KindEndArray {
			if err := r.walkJSON(ctx, sess, pl, dec, enc, child, depth+1, now); err != nil {
				return err
			}
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndArray)

	case jsontext.KindInvalid:
		return io.ErrUnexpectedEOF

	default:
		raw, err := dec.ReadValue()
		if err != nil {
			return err
		}
		out := r.applyLeaf(ctx, sess, pl, path, raw, now)
		return enc.WriteValue(out)
	}
}

// pathLeadsToDeclared reports whether path is declared or is a prefix of a
// declared one, which is when its key may be shown.
func pathLeadsToDeclared(paths map[string]types.ColumnPolicy, path string) bool {
	for p := range paths {
		if p == path || strings.HasPrefix(p, path+".") || strings.HasPrefix(p, path+"[") {
			return true
		}
	}
	return false
}

// applyLeaf applies a path's policy to one scalar leaf.
func (r *Redactor) applyLeaf(ctx context.Context, sess *session, pl *columnPlan, path string, raw jsontext.Value, now time.Time) jsontext.Value {
	p, ok := pl.paths[path]
	if !ok {
		// R5.7: an uncatalogued path is an unknown column. It redacts, and the
		// statement runs.
		return quoteJSON(RedactedMarker)
	}
	policy := p.Policy
	if !policy.Valid() {
		policy = types.PolicyRedact
	}
	if pl.floor != types.PolicyAllow {
		if raised := types.Strictest(policy, pl.floor); raised != policy {
			policy = raised
		}
	}
	if policy == types.PolicyToken && p.Namespace == "" {
		policy = types.PolicyRedact
	}
	if raw.Kind() == jsontext.KindNull {
		return raw
	}
	if pl.scan != nil && pl.scan.collecting {
		if pl.examinesText(policy) && raw.Kind() == jsontext.KindString {
			r.redactSpans(pl, jsonScalarString(raw))
		}
		return raw
	}
	switch policy {
	case types.PolicyToken:
		sub := *pl
		sub.namespace = p.Namespace
		return quoteJSON(r.tokenizeLeaf(sess, pl, &sub, jsonScalarString(raw), now))
	case types.PolicyAllow, types.PolicyScan:
		if raw.Kind() != jsontext.KindString || !pl.examinesText(policy) {
			return raw
		}
		out, n := r.redactSpans(pl, jsonScalarString(raw))
		pl.spans += n
		return quoteJSON(out)
	default:
		return quoteJSON(RedactedMarker)
	}
}

// tokenizeLeaf tokenizes under the path's namespace while counting collisions
// against the column.
func (r *Redactor) tokenizeLeaf(sess *session, col *columnPlan, sub *columnPlan, value string, now time.Time) string {
	out := r.tokenize(sess, sub, value, now)
	col.collisions += sub.collisions
	return out
}

// jsonScalarString renders a scalar leaf as the string a policy operates on: a
// JSON string's decoded contents, or the literal text of a number or boolean.
func jsonScalarString(raw jsontext.Value) string {
	if raw.Kind() == jsontext.KindString {
		var s string
		if err := unquoteInto(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

func unquoteInto(raw jsontext.Value, s *string) error {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	*s = tok.String()
	return nil
}

func quoteJSON(s string) jsontext.Value {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.String(s)); err != nil {
		return jsontext.Value(`""`)
	}
	return jsontext.Value(strings.TrimRight(buf.String(), "\n"))
}
