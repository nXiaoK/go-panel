//go:build !selfupdate_e2e

package fetch

import "testing"

func TestProductionBuildIgnoresE2EReleaseBaseURL(t *testing.T) {
	t.Setenv("GO_PANEL_E2E_RELEASE_BASE_URL", "http://attacker.invalid")
	got, err := (&Client{}).URL(assetURL)
	if err != nil || got != assetURL {
		t.Fatalf("production builds must never rewrite release downloads: %q %v", got, err)
	}
}
