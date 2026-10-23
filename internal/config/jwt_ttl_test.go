package config

import (
	"testing"
	"time"
)

func TestJWTLifetimeParsingAndValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		valid bool
	}{
		{"", 2160 * time.Hour, true}, {"  ", 2160 * time.Hour, true}, {"1h", time.Hour, true}, {"2160h", 2160 * time.Hour, true}, {"24h30m", 24*time.Hour + 30*time.Minute, true},
		{"30m", 0, false}, {"2161h", 0, false}, {"0", 0, false}, {"-1h", 0, false}, {"90d", 0, false}, {"invalid", 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("JWT_TTL", tc.value)
			cfg := Load()
			err := cfg.ValidateJWTLifetime()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && cfg.JwtTTL != tc.want {
				t.Fatalf("TTL=%v want %v", cfg.JwtTTL, tc.want)
			}
		})
	}
}
