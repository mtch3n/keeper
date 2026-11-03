package redact

import (
	"reflect"
	"regexp"
	"strings"
)

// RedactedMarker replaces a value that keeps nothing. It is bracketed like a
// token so that a reader of a transcript can tell at a glance that a cell was
// masked rather than empty, and it is not a token: it resolves to nothing and
// cannot be passed back as a parameter.
const RedactedMarker = TokenOpen + "redacted" + TokenClose

// RedactedSpan renders one matched span inside a scanned value. The type
// survives because "an email was here" is what makes the remaining text
// readable; the value does not.
func RedactedSpan(typ string) string {
	if typ == "" {
		return RedactedMarker
	}
	return TokenOpen + "redacted:" + typ + TokenClose
}

// maxWalkDepth bounds recursion into nested containers. Database output is not
// cyclic, but a bound is cheaper than trusting that.
const maxWalkDepth = 64

// rewriteStrings returns v with every string leaf replaced by fn(leaf).
//
// R8.4c requires the emission scan to reach string leaves inside json, array and
// row outputs, because to_json($1), ARRAY[$1] and ROW($1) are ordinary
// derivations and each of them puts the resolved value one level down. The walk
// is by reflection rather than a type switch so that a driver's own array and
// composite types are covered without this package enumerating them.
//
// Containers are rebuilt rather than mutated: the row slices come from the
// driver and may share backing arrays with buffers this package does not own.
func rewriteStrings(v any, fn func(string) string) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return fn(t)
	case []byte:
		s := fn(string(t))
		if s == string(t) {
			return v
		}
		return []byte(s)
	}
	tooDeep := false
	nv, changed := rewriteValue(reflect.ValueOf(v), fn, 0, &tooDeep)
	if tooDeep {
		// Text below the limit was never looked at, so none of the cell is shown.
		return RedactedMarker
	}
	if !changed {
		return v
	}
	return nv.Interface()
}

func rewriteValue(rv reflect.Value, fn func(string) string, depth int, tooDeep *bool) (reflect.Value, bool) {
	if depth > maxWalkDepth {
		*tooDeep = true
		return rv, false
	}
	if !rv.IsValid() {
		return rv, false
	}
	switch rv.Kind() {
	case reflect.String:
		old := rv.String()
		s := fn(old)
		if s == old {
			return rv, false
		}
		nv := reflect.New(rv.Type()).Elem()
		nv.SetString(s)
		return nv, true

	case reflect.Pointer:
		if rv.IsNil() {
			return rv, false
		}
		ev, changed := rewriteValue(rv.Elem(), fn, depth+1, tooDeep)
		if !changed {
			return rv, false
		}
		np := reflect.New(rv.Type().Elem())
		np.Elem().Set(ev)
		return np, true

	case reflect.Interface:
		if rv.IsNil() {
			return rv, false
		}
		ev, changed := rewriteValue(rv.Elem(), fn, depth+1, tooDeep)
		if !changed {
			return rv, false
		}
		nv := reflect.New(rv.Type()).Elem()
		nv.Set(ev)
		return nv, true

	case reflect.Slice:
		if rv.IsNil() {
			return rv, false
		}
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			old := string(rv.Bytes())
			s := fn(old)
			if s == old {
				return rv, false
			}
			nv := reflect.New(rv.Type()).Elem()
			nv.SetBytes([]byte(s))
			return nv, true
		}
		fallthrough
	case reflect.Array:
		changed := false
		out := reflect.MakeSlice(reflect.SliceOf(rv.Type().Elem()), rv.Len(), rv.Len())
		for i := range rv.Len() {
			ev, c := rewriteValue(rv.Index(i), fn, depth+1, tooDeep)
			if c {
				changed = true
				out.Index(i).Set(ev)
			} else {
				out.Index(i).Set(rv.Index(i))
			}
		}
		if !changed {
			return rv, false
		}
		if rv.Kind() == reflect.Array {
			arr := reflect.New(rv.Type()).Elem()
			reflect.Copy(arr, out)
			return arr, true
		}
		nv := reflect.New(rv.Type()).Elem()
		nv.Set(out)
		return nv, true

	case reflect.Map:
		if rv.IsNil() {
			return rv, false
		}
		changed := false
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k, kc := rewriteValue(iter.Key(), fn, depth+1, tooDeep)
			v, vc := rewriteValue(iter.Value(), fn, depth+1, tooDeep)
			if !kc {
				k = iter.Key()
			}
			if !vc {
				v = iter.Value()
			}
			changed = changed || kc || vc
			out.SetMapIndex(k, v)
		}
		if !changed {
			return rv, false
		}
		return out, true

	case reflect.Struct:
		t := rv.Type()
		changed := false
		out := reflect.New(t).Elem()
		out.Set(rv)
		for i := range t.NumField() {
			if t.Field(i).PkgPath != "" { // unexported: time.Time, netip.Addr
				continue
			}
			fv, c := rewriteValue(rv.Field(i), fn, depth+1, tooDeep)
			if c {
				changed = true
				out.Field(i).Set(fv)
			}
		}
		if !changed {
			return rv, false
		}
		return out, true

	default:
		return rv, false
	}
}

// retokenizer re-tokenizes resolved values found in emitted text. It is R8.4a
// and R8.4c: the check that makes the reverse map something other than a
// de-tokenization oracle.
type retokenizer struct {
	idx  index
	hits int
}

func (t *retokenizer) active() bool {
	return t.idx.matcher.Len() > 0 || len(t.idx.short) > 0
}

// rewrite replaces every occurrence of a resolved value in s with that value's
// token.
//
// Matching is case-insensitive substring matching, not equality: exact matching
// fails on 'Hi ' || $1, format('Hi %s',$1), upper($1), to_json($1), ARRAY[$1]
// and ROW($1) — every ordinary derivation. Values too short to be matched as
// substrings without rewriting unrelated text are matched against the whole
// cell instead.
func (t *retokenizer) rewrite(s string) string {
	if s == "" {
		return s
	}
	protected := t.protectedSpans(s)
	if len(protected) == 0 {
		return t.rewriteText(s, true)
	}
	// keeper's own tokens and markers are not data. Text between them is.
	var b strings.Builder
	at := 0
	for _, loc := range protected {
		b.WriteString(t.rewriteText(s[at:loc[0]], false))
		b.WriteString(s[loc[0]:loc[1]])
		at = loc[1]
	}
	b.WriteString(t.rewriteText(s[at:], false))
	return b.String()
}

var bracketed = regexp.MustCompile(TokenOpen + `[^` + TokenOpen + TokenClose + `]*` + TokenClose)

var markerType = regexp.MustCompile(`^` + TokenOpen + `redacted:([a-z][a-z_]*)` + TokenClose + `$`)

// protectedSpans finds the tokens bound in this session and the markers keeper
// writes. A span only looks like one when it is exactly one: text in brackets
// that merely resembles a marker is data, and is scanned. A marker's type is
// protected unless a bound value is the whole of it.
func (t *retokenizer) protectedSpans(s string) [][]int {
	var out [][]int
	for _, loc := range bracketed.FindAllStringIndex(s, -1) {
		span := s[loc[0]:loc[1]]
		switch {
		case t.idx.tokens[span], span == RedactedMarker:
			out = append(out, loc)
		default:
			if m := markerType.FindStringSubmatch(span); m != nil && !t.coversWhole(m[1]) {
				out = append(out, loc)
			}
		}
	}
	return out
}

func (t *retokenizer) coversWhole(s string) bool {
	if _, ok := t.idx.short[s]; ok {
		return true
	}
	for _, h := range t.idx.matcher.FindAll(s) {
		if h.Start == 0 && h.End == len(s) {
			return true
		}
	}
	return false
}

// rewriteText replaces bound values in text that holds no token. whole says
// the text is the entire cell, which is when a short value may match it.
func (t *retokenizer) rewriteText(s string, whole bool) string {
	if s == "" {
		return s
	}
	if whole && len(t.idx.short) > 0 {
		lower := strings.ToLower(s)
		if b, ok := t.idx.short[lower]; ok {
			t.hits++
			return b.token
		}
		if trimmed := strings.TrimSpace(lower); trimmed != lower {
			if b, ok := t.idx.short[trimmed]; ok {
				t.hits++
				return b.token
			}
		}
	}
	m := t.idx.matcher
	if m.Len() == 0 {
		return s
	}
	hits := ResolveOverlaps(m.FindAll(s))
	if len(hits) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	at := 0
	for _, h := range hits {
		if h.Start < at {
			continue
		}
		b.WriteString(s[at:h.Start])
		b.WriteString(t.idx.owners[h.Pattern].token)
		at = h.End
		t.hits++
	}
	b.WriteString(s[at:])
	return b.String()
}
