package config

import "testing"

func TestLoadNormalizesUpdateStrategy(t *testing.T) {
	t.Setenv("UPDATE_STRATEGY", " Soft ")
	if got := Load().UpdateStrategy; got != "soft" {
		t.Fatalf("UpdateStrategy=%q", got)
	}
	t.Setenv("UPDATE_STRATEGY", "")
	if got := Load().UpdateStrategy; got != "auto" {
		t.Fatalf("default UpdateStrategy=%q, want auto", got)
	}
}

func TestValidateUpdateStrategy(t *testing.T) {
	for _, value := range []string{"", "auto", "soft", "sidecar", "manual"} {
		if err := (Config{UpdateStrategy: value}).ValidateUpdateStrategy(); err != nil {
			t.Fatalf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"watchtower", "on", "Soft"} {
		if err := (Config{UpdateStrategy: value}).ValidateUpdateStrategy(); err == nil {
			t.Fatalf("%q must be rejected", value)
		}
	}
}
