package build

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.0.4", 1},
		{"0.0.4", "0.1.0", -1},
		{"0.1.0", "0.1.0", 0},
		{"v0.1.0", "0.1.0", 0},
		{"0.10.0", "0.9.0", 1},
		{"0.1.0-3-gabc1234", "0.1.0", 1},
		{"0.1.0-3-gabc1234-dirty", "0.1.0-3-gabc1234", 1},
		{"0.1.0", "0.1.0-3-gabc1234", -1},
		{"0.2.0", "0.1.0-3-gabc1234", 1},
		{"0.1.0-10-gabc1234", "0.1.0-2-gabc1234", 1},
		{"dev", "0.0.1", -1},
		{"0.0.1", "dev", 1},
		{"dev", "dev", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
