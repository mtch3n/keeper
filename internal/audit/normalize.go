package audit

import "strings"

// Placeholder replaces every literal in a normalized statement.
const Placeholder = "?"

// UnknownIdentifier replaces a quoted identifier that does not match anything in
// the catalog. The quotes survive so the shape of the statement still reads;
// the text inside does not, because a quoted identifier is user-supplied text
// and can carry PII (R10c).
const UnknownIdentifier = `"?"`

// Normalize strips every literal from a statement, discards every comment, and
// keeps a quoted identifier only where known reports it as a catalogued
// relation or column.
//
// # Why a lexer is allowed here
//
// R7.2 prohibits a keeper-authored SQL lexer in *the enforcement path*:
// admission, routing, classification and redaction all derive from the
// protocol, the plan and the catalog, never from keeper's own reading of
// statement text, because CVE-2026-85788 was hand-written comment handling.
// This lexer is outside that path and R7.2 says so explicitly. Its failure mode
// is a literal surviving into a local append-only log, not a gate bypass, and
// the server offers no alternative: pg_stat_statements would need
// pg_read_all_stats, which G0 refuses.
//
// # What it strips, and why each one is in the list
//
//	'...' and ''        a literal can be PII (R10a) — this is the whole point
//	E'...' with \       the same, with backslash escapes
//	U&'...', B'..', X'..'  the same, in the other literal syntaxes
//	$tag$...$tag$       dollar quoting can hold anything, including quotes
//	123, 1.5e3, 0xff    a nine-digit number is an SSN
//	-- to end of line   R10c's worked example is a comment
//	/* ... */, nested   PostgreSQL nests them, so this must too
//	"identifier"        kept only when known() recognises it (R10c)
//
// A parameter reference ($1, $2) is not a literal and survives: it is the shape
// of the statement, and the value it stood for was never in the text.
//
// known may be nil, in which case no quoted identifier is kept.
func Normalize(sql string, known func(string) bool) string {
	var b strings.Builder
	b.Grow(len(sql))
	pendingSpace := false

	write := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			last := b.String()[b.Len()-1]
			if pendingSpace || (glues(last) && glues(s[0])) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(s)
		pendingSpace = false
	}

	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case isSpace(c):
			pendingSpace = true
			i++

		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			j := strings.IndexAny(sql[i:], "\n\r")
			if j < 0 {
				i = len(sql)
			} else {
				i += j
			}
			// A discarded comment still separated two tokens.
			pendingSpace = true

		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			i = skipBlockComment(sql, i)
			pendingSpace = true

		case c == '\'':
			i = skipQuoted(sql, i, false)
			write(Placeholder)

		case (c == 'e' || c == 'E') && i+1 < len(sql) && sql[i+1] == '\'':
			i = skipQuoted(sql, i+1, true)
			write(Placeholder)

		case (c == 'u' || c == 'U') && i+2 < len(sql) && sql[i+1] == '&' && sql[i+2] == '\'':
			i = skipQuoted(sql, i+2, false)
			write(Placeholder)

		case (c == 'b' || c == 'B' || c == 'x' || c == 'X') && i+1 < len(sql) && sql[i+1] == '\'':
			i = skipQuoted(sql, i+1, false)
			write(Placeholder)

		case c == '$':
			if end, ok := skipDollarQuoted(sql, i); ok {
				i = end
				write(Placeholder)
				break
			}
			if i+1 < len(sql) && isDigit(sql[i+1]) {
				j := i + 1
				for j < len(sql) && isDigit(sql[j]) {
					j++
				}
				write(sql[i:j])
				i = j
				break
			}
			write("$")
			i++

		case c == '"':
			ident, end := readQuotedIdent(sql, i)
			i = end
			if known != nil && known(ident) {
				write(`"` + strings.ReplaceAll(ident, `"`, `""`) + `"`)
			} else {
				write(UnknownIdentifier)
			}

		case isIdentStart(c):
			j := i
			for j < len(sql) && isIdentPart(sql[j]) {
				j++
			}
			write(sql[i:j])
			i = j

		case isDigit(c) || (c == '.' && i+1 < len(sql) && isDigit(sql[i+1])):
			i = skipNumber(sql, i)
			write(Placeholder)

		default:
			write(sql[i : i+1])
			i++
		}
	}
	return strings.TrimSpace(b.String())
}

// hasStrippableLiteral reports whether s still carries something Normalize
// would have removed. It is the backstop [Log.Write] applies: the audit log
// must never receive a literal or a comment (R10a, CONTRACT §4 rule 6), and
// "the caller promised" is not a mechanism.
//
// It looks for the shapes that carry free text — quotes, dollar quoting and
// comment introducers. Normalize's own output contains none of them except the
// double quotes around a catalogued identifier.
func hasStrippableLiteral(s string) bool {
	if strings.ContainsRune(s, '\'') || strings.Contains(s, "--") || strings.Contains(s, "/*") {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '$' {
			if _, ok := skipDollarQuoted(s, i); ok {
				return true
			}
		}
	}
	return false
}

func skipBlockComment(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			depth++
			i += 2
		case s[i] == '*' && i+1 < len(s) && s[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(s) // unterminated: the rest of the statement is comment
}

// skipQuoted consumes a single-quoted literal starting at the opening quote.
// In a standard string ” is an escaped quote; in an E” string a backslash
// escapes the next byte as well.
func skipQuoted(s string, i int, escapes bool) int {
	i++ // opening quote
	for i < len(s) {
		switch {
		case escapes && s[i] == '\\' && i+1 < len(s):
			i += 2
		case s[i] == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// skipDollarQuoted consumes $tag$...$tag$ starting at the first '$'. The tag is
// empty or an identifier; $1 is a parameter reference and is not a tag.
func skipDollarQuoted(s string, i int) (int, bool) {
	j := i + 1
	for j < len(s) && s[j] != '$' {
		if !isIdentPart(s[j]) || isDigit(s[j]) && j == i+1 {
			return 0, false
		}
		j++
	}
	if j >= len(s) {
		return 0, false
	}
	delim := s[i : j+1] // $tag$
	k := strings.Index(s[j+1:], delim)
	if k < 0 {
		return len(s), true // unterminated: everything after it is literal
	}
	return j + 1 + k + len(delim), true
}

// readQuotedIdent returns the identifier's text with "" unescaped, and the
// offset just past the closing quote.
func readQuotedIdent(s string, i int) (string, int) {
	i++ // opening quote
	var b strings.Builder
	for i < len(s) {
		if s[i] == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i += 2
				continue
			}
			return b.String(), i + 1
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), len(s)
}

func skipNumber(s string, i int) int {
	if s[i] == '0' && i+1 < len(s) {
		switch s[i+1] {
		case 'x', 'X', 'o', 'O', 'b', 'B':
			i += 2
			for i < len(s) && (isHex(s[i]) || s[i] == '_') {
				i++
			}
			return i
		}
	}
	for i < len(s) && (isDigit(s[i]) || s[i] == '_') {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && (isDigit(s[i]) || s[i] == '_') {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	return i
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 0x80
}
func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }

// glues reports whether a byte would run into its neighbour if the whitespace
// between them were dropped.
func glues(c byte) bool {
	return isIdentPart(c) || c == '"' || c == '?'
}
