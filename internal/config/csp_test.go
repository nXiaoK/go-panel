package config

import "testing"

func TestCSPModeParsingAndValidation(t *testing.T) {
	for _, tc := range []struct {
		value, want string
		valid       bool
	}{
		{"", "report-only", true}, {"  ", "", true}, {"report-only", "report-only", true},
		{"enforce", "enforce", true}, {" off ", "off", true},
		{"report", "report", false}, {"ENFORCE", "ENFORCE", false},
		{"false", "false", false}, {"enforce,off", "enforce,off", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("CSP_MODE", tc.value)
			cfg := Load()
			if got := cfg.CSPMode; got != tc.want {
				t.Fatalf("mode=%q want %q", got, tc.want)
			}
			if err := cfg.ValidateCSPMode(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
