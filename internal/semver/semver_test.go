package semver

import "testing"

func TestValid(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.3":      true,
		"0.1.0":      true,
		"1.2.3-rc.1": true,
		"v1.2.3":     false,
		"1.2":        false,
		"1":          false,
		"1.2.3+b5":   false,
		"01.2.3":     false,
		"":           false,
		"latest":     false,
	} {
		if got := Valid(v); got != want {
			t.Errorf("Valid(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.1.0", -1},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0-rc.1", "2.0.0", -1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestBump(t *testing.T) {
	tests := []struct {
		v    string
		step Step
		want string
	}{
		{"1.2.3", Patch, "1.2.4"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Major, "2.0.0"},
		{"1.3.0-rc.1", Patch, "1.3.1"},
	}
	for _, tt := range tests {
		got, err := Bump(tt.v, tt.step)
		if err != nil || got != tt.want {
			t.Errorf("Bump(%s, %s) = %q, %v; want %q", tt.v, tt.step, got, err, tt.want)
		}
	}
	if _, err := Bump("1.2", Minor); err == nil {
		t.Error("Bump of an invalid version must fail")
	}
	if _, err := Bump("1.2.3", Step("huge")); err == nil {
		t.Error("Bump with an unknown step must fail")
	}
}
