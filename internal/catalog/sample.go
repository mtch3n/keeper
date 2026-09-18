package catalog

// sampleTokenThreshold is SPEC R5.3a and R5.3b's shared bulk-accept
// threshold: at or above this share of non-null samples, catalog init
// proposes token instead of scan. Below it and above zero, it proposes
// scan and reports the rate — raising `notes` (measured 10%) or `memo`
// (measured 2%) to token would mask whole rows where span redaction already
// handles the hits.
const sampleTokenThreshold = 0.90

// digitsOnly strips everything but ASCII digits, so "123-45-6789" and
// "123456789" test the same under isNineDigit and luhnValid.
func digitsOnly(s string) string {
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		if c := s[i]; c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	return string(out)
}

// isNineDigit reports whether s, once punctuation is stripped, is exactly
// nine digits — the shape of a US SSN. SPEC R5.3b.
func isNineDigit(s string) bool {
	return len(digitsOnly(s)) == 9
}

// luhnValid reports whether s, once punctuation is stripped, passes the Luhn
// checksum — the shape of a card number. SPEC R5.3b. Measured Luhn
// acceptance on random integers is around 10% (SPEC R5.6c), which is exactly
// why this test alone is never enough to justify tokenizing a key column.
func luhnValid(s string) bool {
	d := digitsOnly(s)
	if len(d) < 2 {
		return false
	}
	sum := 0
	double := false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}
