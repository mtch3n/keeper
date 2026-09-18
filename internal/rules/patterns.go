package rules

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mtchen/keeper/internal/ports"
)

// The type labels this package attaches to a span. The label is not decoration:
// SPEC R5.3a makes the rule that matched the token namespace, which is what
// closes the "token with no namespace" defect (R5.2b). Two columns matched by
// the same rule therefore land in the same namespace and still join.
const (
	TypeEmail      = "email"
	TypePhone      = "phone"
	TypeCard       = "credit_card"
	TypeIBAN       = "iban"
	TypeIP         = "ip"
	TypeURL        = "url"
	TypeUUID       = "uuid"
	TypeUSSSN      = "us_ssn"
	TypePersonName = "person_name"
)

// Types is every label this package can emit, in priority order: where two
// rules overlap the earlier one wins. It is deliberately a short list, and
// [Coverage] says what being absent from it means.
var Types = []string{
	TypeEmail, TypeURL, TypeIBAN, TypeCard, TypeUUID, TypeIP, TypeUSSSN, TypePhone, TypePersonName,
}

type pattern struct {
	typ string
	re  *regexp.Regexp
	// check validates the regexp hit and may narrow it. It returns the final
	// span and a score, or ok=false to reject.
	check func(text string, start, end int) (int, int, float64, bool)
}

var (
	reEmail     = regexp.MustCompile(`[A-Za-z0-9!#$%&'*+/=?^_{|}~.-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+`)
	reURL       = regexp.MustCompile(`(?i)\b(?:https?|ftps?|ssh|sftp|mailto|postgres(?:ql)?|redis|mongodb)://[^\s<>"'` + "`" + `]+`)
	reIBAN      = regexp.MustCompile(`\b[A-Za-z]{2}[0-9]{2}(?:[ -]?[A-Za-z0-9]){10,32}\b`)
	reCard      = regexp.MustCompile(`\b[0-9](?:[ -]?[0-9]){11,18}\b`)
	reUUID      = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reIPv4      = regexp.MustCompile(`\b[0-9]{1,3}(?:\.[0-9]{1,3}){3}\b`)
	reIPv6      = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{1,4}\b|::(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){1,7}:`)
	reSSNSep    = regexp.MustCompile(`\b[0-9]{3}[ -][0-9]{2}[ -][0-9]{4}\b`)
	reSSNRaw    = regexp.MustCompile(`\b[0-9]{9}\b`)
	rePhoneIntl = regexp.MustCompile(`\+[0-9][0-9 ().-]{6,20}[0-9]`)
	rePhoneNANP = regexp.MustCompile(`(?:\(([2-9][0-9]{2})\)|\b([2-9][0-9]{2}))[ .-]?[2-9][0-9]{2}[ .-][0-9]{4}\b`)
)

// patterns is the rule table, in the priority order of [Types].
var patterns = []pattern{
	{TypeEmail, reEmail, checkEmail},
	{TypeURL, reURL, checkURL},
	{TypeIBAN, reIBAN, checkIBAN},
	{TypeCard, reCard, checkCard},
	{TypeUUID, reUUID, always(1)},
	{TypeIP, reIPv4, checkIP},
	{TypeIP, reIPv6, checkIP},
	{TypeUSSSN, reSSNSep, checkSSNSeparated},
	{TypeUSSSN, reSSNRaw, checkSSNBare},
	{TypePhone, rePhoneIntl, checkPhoneIntl},
	{TypePhone, rePhoneNANP, checkPhoneNANP},
}

func always(score float64) func(string, int, int) (int, int, float64, bool) {
	return func(_ string, s, e int) (int, int, float64, bool) { return s, e, score, true }
}

// scanPatterns runs every rule and returns the surviving spans, overlaps
// resolved by priority first and position second.
func scanPatterns(text string) []ports.Span {
	var out []ports.Span
	for _, p := range patterns {
		for _, loc := range p.re.FindAllStringIndex(text, -1) {
			s, e, score, ok := p.check(text, loc[0], loc[1])
			if !ok || e <= s {
				continue
			}
			out = append(out, ports.Span{Start: s, End: e, Type: p.typ, Score: score})
		}
	}
	return out
}

// checkEmail rejects a hit that is a suffix of a longer word and enforces the
// structural limits RFC 5321 puts on the two halves.
func checkEmail(text string, s, e int) (int, int, float64, bool) {
	if s > 0 && isLocalPartByte(text[s-1]) {
		return 0, 0, 0, false
	}
	m := text[s:e]
	at := strings.LastIndexByte(m, '@')
	if at <= 0 || at > 64 || len(m) > 254 {
		return 0, 0, 0, false
	}
	local, domain := m[:at], m[at+1:]
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return 0, 0, 0, false
	}
	dot := strings.LastIndexByte(domain, '.')
	if dot < 0 {
		return 0, 0, 0, false
	}
	tld := domain[dot+1:]
	if len(tld) < 2 {
		return 0, 0, 0, false
	}
	for i := range len(tld) {
		if !unicode.IsLetter(rune(tld[i])) {
			return 0, 0, 0, false
		}
	}
	return s, e, 1, true
}

func isLocalPartByte(c byte) bool {
	return c >= 0x80 || c == '@' || strings.IndexByte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&'*+/=?^_{|}~.-", c) >= 0
}

func checkURL(text string, s, e int) (int, int, float64, bool) {
	m := text[s:e]
	for len(m) > 0 && strings.IndexByte(".,;:!?)]}\"'", m[len(m)-1]) >= 0 {
		m = m[:len(m)-1]
		e--
	}
	u, err := url.Parse(m)
	if err != nil || u.Scheme == "" {
		return 0, 0, 0, false
	}
	if u.Opaque == "" && u.Host == "" {
		return 0, 0, 0, false
	}
	return s, e, 1, true
}

func checkIBAN(text string, s, e int) (int, int, float64, bool) {
	if !ValidIBAN(text[s:e]) {
		return 0, 0, 0, false
	}
	return s, e, 1, true
}

func checkCard(text string, s, e int) (int, int, float64, bool) {
	d := digitsOnly(text[s:e])
	if _, ok := ValidCard(d); !ok {
		return 0, 0, 0, false
	}
	return s, e, 1, true
}

func checkIP(text string, s, e int) (int, int, float64, bool) {
	addr, err := netip.ParseAddr(text[s:e])
	if err != nil || addr.IsUnspecified() {
		return 0, 0, 0, false
	}
	// A bare "1.2.3.4" inside a version string is indistinguishable from an
	// address; that is inherent, and the span is what gets redacted, not the row.
	return s, e, 1, true
}

func checkSSNSeparated(text string, s, e int) (int, int, float64, bool) {
	if !ValidSSN(digitsOnly(text[s:e])) {
		return 0, 0, 0, false
	}
	return s, e, 0.9, true
}

// checkSSNBare scores lower than the separated form: nine digits with no
// separators is also an order id, and R5.3b's key-column test — not this rule —
// is what tells them apart at catalog time.
func checkSSNBare(text string, s, e int) (int, int, float64, bool) {
	if !ValidSSN(text[s:e]) {
		return 0, 0, 0, false
	}
	return s, e, 0.4, true
}

func checkPhoneIntl(text string, s, e int) (int, int, float64, bool) {
	d := digitsOnly(text[s:e])
	if len(d) < 8 || len(d) > 15 { // ITU-T E.164 bounds
		return 0, 0, 0, false
	}
	for e > s && strings.IndexByte(" .-()", text[e-1]) >= 0 {
		e--
	}
	return s, e, 0.85, true
}

func checkPhoneNANP(text string, s, e int) (int, int, float64, bool) {
	if s > 0 && (isDigit(text[s-1]) || text[s-1] == '+') {
		return 0, 0, 0, false
	}
	d := digitsOnly(text[s:e])
	if len(d) != 10 {
		return 0, 0, 0, false
	}
	// NANP: area and exchange both start 2-9, and N11 area codes are service
	// codes rather than subscriber numbers.
	if d[0] < '2' || d[3] < '2' || (d[1] == '1' && d[2] == '1') {
		return 0, 0, 0, false
	}
	return s, e, 0.9, true
}

// resolveSpans reduces spans to a non-overlapping set. Priority follows [Types]
// order, so an email inside a URL keeps the more specific label, and position
// breaks a tie between equals.
func resolveSpans(in []ports.Span) []ports.Span {
	if len(in) < 2 {
		return in
	}
	rank := make(map[string]int, len(Types))
	for i, t := range Types {
		rank[t] = i
	}
	slices.SortStableFunc(in, func(a, b ports.Span) int {
		if ra, rb := rank[a.Type], rank[b.Type]; ra != rb {
			return ra - rb
		}
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return (b.End - b.Start) - (a.End - a.Start)
	})
	var taken []ports.Span
	for _, sp := range in {
		overlap := false
		for _, t := range taken {
			if sp.Start < t.End && t.Start < sp.End {
				overlap = true
				break
			}
		}
		if !overlap {
			taken = append(taken, sp)
		}
	}
	slices.SortFunc(taken, func(a, b ports.Span) int { return a.Start - b.Start })
	return taken
}

// wordBoundary reports whether the byte offsets s and e sit on the edges of a
// word, treating the characters an identifier can run into — letters, digits,
// and the punctuation that binds an email or a path together — as interior.
func wordBoundary(text string, s, e int) bool {
	if s > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:s])
		if isWordRune(r) {
			return false
		}
	}
	if e < len(text) {
		r, _ := utf8.DecodeRuneInString(text[e:])
		if isWordRune(r) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '@', '.', '-', '+', '/':
		return true
	}
	return false
}
