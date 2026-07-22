package httpapi

import "testing"

func TestValidateRepoURL(t *testing.T) {
	if err := validateRepoURL("https://github.com/you/repo"); err != nil {
		t.Errorf("valid https URL rejected: %v", err)
	}

	rejected := map[string]string{
		"":                        "empty",
		"git@github.com:you/repo": "ssh shorthand",
		"http://github.com/a/b":   "plain http",
		"ssh://git@github.com/a":  "ssh scheme",
		"https://":                "no host",
	}
	for raw, why := range rejected {
		if err := validateRepoURL(raw); err == nil {
			t.Errorf("validateRepoURL(%q) = nil, want error (%s)", raw, why)
		}
	}
}

func TestValidateEnvKey(t *testing.T) {
	for _, k := range []string{"PORT", "NODE_ENV", "_private", "a1"} {
		if err := validateEnvKey(k); err != nil {
			t.Errorf("validateEnvKey(%q) = %v, want nil", k, err)
		}
	}
	for _, k := range []string{"", "bad-key", "has space", "1PORT", "a.b"} {
		if err := validateEnvKey(k); err == nil {
			t.Errorf("validateEnvKey(%q) = nil, want error", k)
		}
	}
}
