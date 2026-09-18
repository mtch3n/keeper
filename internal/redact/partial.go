package redact

import (
	"net/netip"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// Mask is the character a partial form replaces a component with.
const Mask = "█" // █

// fixedMask is what every variable-length hidden component becomes. It is a
// fixed width on purpose: a per-character mask leaks the length of what it hid,
// and the length of a local part or a subscriber number is information about
// the value.
const fixedMask = Mask + Mask + Mask + Mask

// Partial keeps exactly the component form declares and nothing else.
//
// The set of forms is closed (R5.2d) rather than a per-column format string,
// because a format string is where someone writes "keep the first nine
// characters" against an SSN. Adding a form is a change to keeper, reviewed
// once; it is not a change to a catalog, reviewed by whoever is editing YAML at
// the time.
//
// A value that is not of the shape the form names keeps nothing: Partial
// returns [RedactedMarker]. "Keeps exactly the declared component" has to mean
// that a phone number in the card column discloses no digits, and the only way
// to guarantee it is to refuse to guess.
//
// partial is the weaker of partial and token, and §8.2 says what it costs:
// equality does not survive, so COUNT(DISTINCT) over a partial email column
// counts domains and not people, and the emitted string is not a token and
// cannot be passed back as a parameter.
func Partial(form types.PartialForm, value string) string {
	switch form {
	case types.FormEmailDomain:
		return partialEmail(value)
	case types.FormCardBINLast4:
		return partialCard(value)
	case types.FormPhoneCountryArea:
		return partialPhone(value)
	case types.FormIPNetwork:
		return partialIP(value)
	default:
		return RedactedMarker
	}
}

// partialEmail keeps the domain: ████@acme.example.
func partialEmail(v string) string {
	s := strings.TrimSpace(v)
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return RedactedMarker
	}
	domain := s[at+1:]
	if !strings.Contains(domain, ".") || strings.ContainsAny(domain, " \t@") {
		return RedactedMarker
	}
	return fixedMask + "@" + domain
}

// partialCard keeps the issuer identifier and the last four: 453211██████3333.
//
// The middle is masked digit for digit rather than at a fixed width, because the
// total length of a card number is a property of its issuer range and is already
// disclosed by the six digits that survive.
func partialCard(v string) string {
	var digits strings.Builder
	for i := range len(v) {
		c := v[i]
		switch {
		case c >= '0' && c <= '9':
			digits.WriteByte(c)
		case c == ' ' || c == '-':
		default:
			return RedactedMarker
		}
	}
	d := digits.String()
	if len(d) < 12 || len(d) > 19 {
		return RedactedMarker
	}
	return d[:6] + strings.Repeat(Mask, len(d)-10) + d[len(d)-4:]
}

// partialPhone keeps the country code, and the area code where the numbering
// plan defines one at a fixed length: +1-978-████.
//
// Only the North American Numbering Plan gets an area code here. Elsewhere the
// area code is variable-length and identifying it needs libphonenumber's
// national plans, which this build does not carry (§8.5.1); keeping the country
// code alone is the honest answer rather than guessing a split.
func partialPhone(v string) string {
	s := strings.TrimSpace(v)
	plus := strings.HasPrefix(s, "+")
	var digits strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits.WriteByte(c)
		case c == '+' && i == 0:
		case c == ' ' || c == '-' || c == '.' || c == '(' || c == ')':
		default:
			return RedactedMarker
		}
	}
	d := digits.String()
	if len(d) < 8 || len(d) > 15 { // ITU-T E.164
		return RedactedMarker
	}
	cc, rest, ok := splitCountryCode(d, plus)
	if !ok {
		return RedactedMarker
	}
	if cc == "1" && len(rest) == 10 && rest[0] >= '2' {
		return "+1-" + rest[:3] + "-" + fixedMask
	}
	return "+" + cc + "-" + fixedMask
}

// e164TwoDigitZones are the ITU-T E.164 assignments that are two digits long.
// Zones 1 and 7 are one digit; everything else in an assigned zone is three.
var e164TwoDigitZones = map[string]bool{
	"20": true, "27": true,
	"30": true, "31": true, "32": true, "33": true, "34": true, "36": true, "39": true,
	"40": true, "41": true, "43": true, "44": true, "45": true, "46": true, "47": true, "48": true, "49": true,
	"51": true, "52": true, "53": true, "54": true, "55": true, "56": true, "57": true, "58": true,
	"60": true, "61": true, "62": true, "63": true, "64": true, "65": true, "66": true,
	"81": true, "82": true, "84": true, "86": true,
	"90": true, "91": true, "92": true, "93": true, "94": true, "95": true, "98": true,
}

// splitCountryCode applies ITU-T E.164's zone rules. Without a leading + the
// number is only unambiguous when it carries a NANP national number, so a bare
// ten-digit string is read as NANP and anything else refuses.
func splitCountryCode(d string, plus bool) (cc, rest string, ok bool) {
	if !plus {
		switch {
		case len(d) == 10:
			return "1", d, true
		case len(d) == 11 && d[0] == '1':
			return "1", d[1:], true
		default:
			return "", "", false
		}
	}
	switch {
	case d[0] == '1' || d[0] == '7':
		return d[:1], d[1:], true
	case len(d) >= 2 && e164TwoDigitZones[d[:2]]:
		return d[:2], d[2:], true
	case len(d) >= 3:
		return d[:3], d[3:], true
	default:
		return "", "", false
	}
}

// partialIP keeps the network: 192.168.2.███ for IPv4, the /48 for IPv6.
func partialIP(v string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return RedactedMarker
	}
	if addr.Is4() || addr.Is4In6() {
		b := addr.As4()
		p := netip.AddrFrom4([4]byte{b[0], b[1], b[2], 0}).String()
		return strings.TrimSuffix(p, "0") + Mask + Mask + Mask
	}
	b := addr.As16()
	var trunc [16]byte
	copy(trunc[:6], b[:6]) // the /48 site prefix
	p := netip.AddrFrom16(trunc).String()
	p = strings.TrimSuffix(p, "::")
	return p + "::" + Mask + Mask + Mask
}
