package domain

import "testing"

func TestValidateAppName(t *testing.T) {
	valid := []string{"api", "my-api", "web2", "a1b2c3", "abcdefghij0123456789012345678"}
	for _, name := range valid {
		if err := ValidateAppName(name); err != nil {
			t.Errorf("ValidateAppName(%q) = %v, want nil", name, err)
		}
	}

	invalid := map[string]string{
		"ab":                              "too short",
		"":                                "empty",
		"1api":                            "starts with a digit",
		"-api":                            "starts with a hyphen",
		"api-":                            "ends with a hyphen",
		"My-API":                          "uppercase",
		"my_api":                          "underscore",
		"my.api":                          "dot",
		"abcdefghij01234567890123456789x": "too long",
	}
	for name, why := range invalid {
		if err := ValidateAppName(name); err == nil {
			t.Errorf("ValidateAppName(%q) = nil, want error (%s)", name, why)
		}
	}
}

func TestNewIDIsUniqueAndURLSafe(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := NewID()
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate: %q", id)
		}
		seen[id] = true

		if len(id) != 26 {
			t.Fatalf("NewID() = %q, want 26 chars", id)
		}
		for _, r := range id {
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '2' && r <= '7' // base32 alphabet
			if !isLower && !isDigit {
				t.Fatalf("NewID() = %q contains non-base32 rune %q", id, r)
			}
		}
	}
}

func TestProbeTimeoutNeverExceedsPeriod(t *testing.T) {
	// A timeout longer than the period lets checks overlap, which Kubernetes
	// warns about and which makes failure counting unpredictable.
	cases := map[int32]int32{
		1:   1,
		3:   3,
		5:   5,
		10:  5,
		300: 5,
		0:   5, // Unset falls back to the cap rather than to zero.
	}
	for period, want := range cases {
		got := Health{PeriodSeconds: period}.ProbeTimeout()
		if got != want {
			t.Errorf("period %ds: ProbeTimeout() = %d, want %d", period, got, want)
		}
		if period > 0 && got > period {
			t.Errorf("period %ds: timeout %ds exceeds the period", period, got)
		}
	}
}

func TestValidateHealth(t *testing.T) {
	ok := Health{Type: HealthHTTP, Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10}
	if err := ValidateHealth(ok); err != nil {
		t.Errorf("valid health rejected: %v", err)
	}

	bad := map[string]Health{
		"period zero":     {Type: HealthTCP, PeriodSeconds: 0},
		"period too long": {Type: HealthTCP, PeriodSeconds: 301},
		"path without /":  {Type: HealthHTTP, Path: "healthz", PeriodSeconds: 10},
		"unknown type":    {Type: "grpc", PeriodSeconds: 10},
		"negative delay":  {Type: HealthTCP, PeriodSeconds: 10, InitialDelaySeconds: -1},
	}
	for name, h := range bad {
		if err := ValidateHealth(h); err == nil {
			t.Errorf("%s: ValidateHealth() = nil, want error", name)
		}
	}
}
