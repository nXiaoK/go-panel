package nftgeneration

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSharedGenerationCoordinationPaths(t *testing.T) {
	t.Parallel()

	if ActiveTableMarkerPath != "/var/lib/flux-nftables/active-table" {
		t.Fatalf("ActiveTableMarkerPath=%q", ActiveTableMarkerPath)
	}
	if LockPath != "/run/flux-nftables/flow-reporter.lock" {
		t.Fatalf("LockPath=%q", LockPath)
	}
}

func TestValidateUniqueJSONRejectsDuplicateKeysAndTrailingData(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"nftables":[],"nftables":[]}`,
		`{"nftables":[{"table":{"name":"flux_panel","name":"other"}}]}`,
		`{"nftables":[]} {}`,
	} {
		if err := ValidateUniqueJSON([]byte(raw)); err == nil {
			t.Errorf("ValidateUniqueJSON accepted %s", raw)
		}
	}
	if err := ValidateUniqueJSON([]byte(`{"nftables":[{"table":{"name":"flux_panel"}}]}`)); err != nil {
		t.Fatalf("ValidateUniqueJSON valid input: %v", err)
	}
}

func TestValidateTableNameAcceptsOnlyLegacyOrExactGeneration(t *testing.T) {
	t.Parallel()

	valid := []string{
		LegacyTable,
		"flux_panel_g_00112233445566778899aabbccddeeff",
		"flux_panel_g_ffffffffffffffffffffffffffffffff",
	}
	for _, table := range valid {
		if err := ValidateTableName(table); err != nil {
			t.Errorf("ValidateTableName(%q): %v", table, err)
		}
	}

	invalid := []string{
		"", "flux_panel ", " flux_panel", "FLUX_PANEL",
		"flux_panel_g_", "flux_panel_g_00112233445566778899aabbccddee",
		"flux_panel_g_00112233445566778899aabbccddeeff00",
		"flux_panel_g_00112233445566778899AABBCCDDEEFF",
		"flux_panel_g_00112233445566778899aabbccddeefg",
		"flux_panel;flush ruleset", "other",
	}
	for _, table := range invalid {
		if err := ValidateTableName(table); err == nil {
			t.Errorf("ValidateTableName(%q) returned nil", table)
		}
	}
}

func TestNewTableNameReadsExactlySixteenBytes(t *testing.T) {
	t.Parallel()

	input := append([]byte(nil), []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	}...)
	r := bytes.NewReader(append(input, 0x42))
	got, err := NewTableName(r)
	if err != nil {
		t.Fatalf("NewTableName: %v", err)
	}
	if want := "flux_panel_g_00112233445566778899aabbccddeeff"; got != want {
		t.Fatalf("NewTableName=%q, want %q", got, want)
	}
	if r.Len() != 1 {
		t.Fatalf("reader remaining bytes=%d, want 1", r.Len())
	}
}

func TestNewTableNameFailsClosedOnShortOrFailingReader(t *testing.T) {
	t.Parallel()

	for _, r := range []interface{ Read([]byte) (int, error) }{
		bytes.NewReader(make([]byte, 15)),
		errorReader{err: errors.New("entropy unavailable")},
	} {
		if got, err := NewTableName(r); err == nil || got != "" {
			t.Errorf("NewTableName(%T)=(%q, %v), want empty name and error", r, got, err)
		}
	}
}

func TestRewriteCanonicalRuleChangesOnlyValidatedTableToken(t *testing.T) {
	t.Parallel()

	const validRule = `add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 counter accept`
	const target = "flux_panel_g_00112233445566778899aabbccddeeff"
	got, err := RewriteCanonicalRule(validRule, target)
	if err != nil {
		t.Fatalf("RewriteCanonicalRule: %v", err)
	}
	if want := strings.Replace(validRule, LegacyTable, target, 1); got != want {
		t.Fatalf("rewritten rule=%q, want %q", got, want)
	}
	if got, err := RewriteCanonicalRule(validRule, LegacyTable); err != nil || got != validRule {
		t.Fatalf("legacy rewrite=(%q, %v), want original", got, err)
	}
}

func TestRewriteCanonicalRuleRejectsUnsafeOrNonCanonicalInput(t *testing.T) {
	t.Parallel()

	const validRule = `add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 counter accept`
	badRules := []string{
		"flux_panel;flush ruleset",
		"other",
		validRule + "\n",
		validRule + "\rflush ruleset",
		strings.Replace(validRule, " forward ", "\tforward ", 1),
		strings.Replace(validRule, "add rule", "add  rule", 1),
		strings.Replace(validRule, LegacyTable, "other", 1),
		strings.Replace(validRule, "forward", "input", 1),
		"add rule inet flux_panel forward",
	}
	for _, rule := range badRules {
		if _, err := RewriteCanonicalRule(rule, LegacyTable); err == nil {
			t.Errorf("RewriteCanonicalRule(%q) returned nil", rule)
		}
	}

	for _, target := range []string{"", "other", "flux_panel;flush ruleset"} {
		if _, err := RewriteCanonicalRule(validRule, target); err == nil {
			t.Errorf("RewriteCanonicalRule accepted target %q", target)
		}
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
