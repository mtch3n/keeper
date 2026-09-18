package catalog

import "testing"

func TestIsNineDigit(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123456789", true},
		{"123-45-6789", true}, // punctuation stripped
		{"12345678", false},
		{"1234567890", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isNineDigit(tc.in); got != tc.want {
			t.Errorf("isNineDigit(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLuhnValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"4111111111111111", true},  // well-known Visa test number
		{"4111111111111112", false}, // one digit off
		{"79927398713", true},       // the canonical Luhn worked example
		{"1234567890123456", false},
		{"1", false}, // too short to mean anything
		{"", false},
	}
	for _, tc := range cases {
		if got := luhnValid(tc.in); got != tc.want {
			t.Errorf("luhnValid(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
