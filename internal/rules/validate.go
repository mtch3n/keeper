package rules

import "strings"

// Luhn reports whether digits passes the Luhn check (ISO/IEC 7812-1 Annex B),
// the check digit every payment card carries. digits must already be stripped
// of separators; a non-digit byte fails.
//
// Luhn alone is a weak filter — roughly one in ten random digit runs of the
// right length passes it — which is why [ValidCard] also requires a published
// issuer identifier range and the length that range declares.
func Luhn(digits string) bool {
	if len(digits) < 2 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// cardBrand names the issuer range a number falls in, with the lengths ISO/IEC
// 7812 and the schemes publish for it.
type cardBrand struct {
	name    string
	lengths []int
	match   func(d string) bool
}

func hasPrefixRange(d string, lo, hi int, n int) bool {
	if len(d) < n {
		return false
	}
	v := 0
	for i := range n {
		v = v*10 + int(d[i]-'0')
	}
	return v >= lo && v <= hi
}

var cardBrands = []cardBrand{
	{"visa", []int{13, 16, 19}, func(d string) bool { return strings.HasPrefix(d, "4") }},
	{"mastercard", []int{16}, func(d string) bool {
		return hasPrefixRange(d, 51, 55, 2) || hasPrefixRange(d, 2221, 2720, 4)
	}},
	{"amex", []int{15}, func(d string) bool { return hasPrefixRange(d, 34, 34, 2) || hasPrefixRange(d, 37, 37, 2) }},
	{"discover", []int{16, 19}, func(d string) bool {
		return strings.HasPrefix(d, "6011") || hasPrefixRange(d, 644, 649, 3) ||
			hasPrefixRange(d, 65, 65, 2) || hasPrefixRange(d, 622126, 622925, 6)
	}},
	{"jcb", []int{16, 17, 18, 19}, func(d string) bool { return hasPrefixRange(d, 3528, 3589, 4) }},
	{"diners", []int{14, 16, 19}, func(d string) bool {
		return hasPrefixRange(d, 300, 305, 3) || strings.HasPrefix(d, "3095") ||
			hasPrefixRange(d, 36, 36, 2) || hasPrefixRange(d, 38, 39, 2)
	}},
	{"unionpay", []int{16, 17, 18, 19}, func(d string) bool { return hasPrefixRange(d, 62, 62, 2) }},
	{"maestro", []int{12, 13, 14, 15, 16, 17, 18, 19}, func(d string) bool {
		return strings.HasPrefix(d, "5018") || strings.HasPrefix(d, "5020") ||
			strings.HasPrefix(d, "5038") || strings.HasPrefix(d, "5893") ||
			strings.HasPrefix(d, "6304") || strings.HasPrefix(d, "6759") ||
			strings.HasPrefix(d, "6761") || strings.HasPrefix(d, "6762") ||
			strings.HasPrefix(d, "6763")
	}},
}

// ValidCard reports whether digits is a payment card number: a published issuer
// identifier range, the length that range declares, and a passing Luhn check.
// It also returns the brand, which is what makes a hit explainable.
func ValidCard(digits string) (brand string, ok bool) {
	if len(digits) < 12 || len(digits) > 19 {
		return "", false
	}
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return "", false
		}
	}
	if !Luhn(digits) {
		return "", false
	}
	for _, b := range cardBrands {
		if b.match(digits) && containsInt(b.lengths, len(digits)) {
			return b.name, true
		}
	}
	return "", false
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ibanLength is the ISO 13616 registry's declared total length per country.
// It is a published registry, not a guess about shape; a country absent from it
// is validated on MOD-97 alone.
var ibanLength = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22,
	"BH": 22, "BI": 27, "BR": 29, "BY": 28, "CH": 21, "CR": 22, "CY": 28, "CZ": 24,
	"DE": 22, "DJ": 27, "DK": 18, "DO": 28, "EE": 20, "EG": 29, "ES": 24, "FI": 18,
	"FK": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27,
	"GT": 28, "HN": 28, "HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26,
	"IT": 27, "JO": 30, "KW": 30, "KZ": 20, "LB": 28, "LC": 32, "LI": 21, "LT": 20,
	"LU": 20, "LV": 21, "LY": 25, "MC": 27, "MD": 24, "ME": 22, "MK": 19, "MN": 20,
	"MR": 27, "MT": 31, "MU": 30, "NI": 28, "NL": 18, "NO": 15, "OM": 23, "PK": 24,
	"PL": 28, "PS": 29, "PT": 25, "QA": 29, "RO": 24, "RS": 22, "RU": 33, "SA": 24,
	"SC": 31, "SD": 18, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "SO": 23, "ST": 25,
	"SV": 28, "TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20,
	"YE": 30,
}

// ValidIBAN reports whether s is an IBAN: a registry country code, the length
// that country declares, and a passing MOD-97-10 check (ISO 13616 / ISO 7064).
// Spaces are ignored, which is how IBANs are printed.
func ValidIBAN(s string) bool {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '-' {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
	}
	t := b.String()
	if len(t) < 15 || len(t) > 34 {
		return false
	}
	if !isUpper(t[0]) || !isUpper(t[1]) || !isDigit(t[2]) || !isDigit(t[3]) {
		return false
	}
	if want, ok := ibanLength[t[:2]]; ok && want != len(t) {
		return false
	}
	// MOD-97-10: move the first four characters to the end, map letters to
	// 10..35, and require the whole number to be 1 modulo 97.
	rearranged := t[4:] + t[:4]
	rem := 0
	for i := range len(rearranged) {
		c := rearranged[i]
		switch {
		case isDigit(c):
			rem = rem*10 + int(c-'0')
		case isUpper(c):
			rem = rem*100 + int(c-'A') + 10
		default:
			return false
		}
		rem %= 97
	}
	return rem == 1
}

// ValidSSN reports whether a nine-digit string is a *possible* US Social
// Security number. There is no checksum: the SSA randomized issuance in 2011,
// so the only published rules are which values were never issued — area 000,
// 666 and 900-999, group 00, serial 0000.
//
// This is a shape test and nothing more. It is listed as "format only" in
// [Coverage] for that reason.
func ValidSSN(digits string) bool {
	if len(digits) != 9 {
		return false
	}
	for i := range 9 {
		if !isDigit(digits[i]) {
			return false
		}
	}
	area := digits[:3]
	if area == "000" || area == "666" || area[0] == '9' {
		return false
	}
	if digits[3:5] == "00" {
		return false
	}
	if digits[5:] == "0000" {
		return false
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		if isDigit(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
