package service

import "testing"

func TestParseTargetAddressRejectsNftInjection(t *testing.T) {
	inputs := []string{
		"[10.0.0.1" + string(rune(10)) + "flush ruleset #]:80",
		"10.0.0.1;flush-ruleset:80",
		"10.0.0.1 #comment:80",
		`10.0.0.1"quote:80`,
		"10.0.0.1'quote:80",
		"example.com:80",
		"[fe80::1%eth0]:80",
	}
	for _, input := range inputs {
		if _, err := ParseTargetAddress(input, true); err == nil {
			t.Fatalf("nft target %q must fail", input)
		}
	}
}

func TestParseTargetAddressNormalizesLiteralAddresses(t *testing.T) {
	tests := []struct {
		input      string
		wantHost   string
		wantPort   int
		wantTarget string
	}{
		{input: "192.0.2.1:443", wantHost: "192.0.2.1", wantPort: 443, wantTarget: "192.0.2.1:443"},
		{input: "[2001:0db8::1]:53", wantHost: "2001:db8::1", wantPort: 53, wantTarget: "[2001:db8::1]:53"},
	}

	for _, tt := range tests {
		got, err := ParseTargetAddress(tt.input, true)
		if err != nil {
			t.Fatalf("ParseTargetAddress(%q): %v", tt.input, err)
		}
		if got.Host != tt.wantHost || got.Port != tt.wantPort || got.Normalized != tt.wantTarget {
			t.Fatalf("ParseTargetAddress(%q) = %#v, want host=%q port=%d normalized=%q", tt.input, got, tt.wantHost, tt.wantPort, tt.wantTarget)
		}
		if !got.IP.IsValid() {
			t.Fatalf("ParseTargetAddress(%q) returned invalid IP", tt.input)
		}
	}
}

func TestParseTargetAddressAcceptsStrictIDNAForGost(t *testing.T) {
	got, err := ParseTargetAddress("BÜCHER.example:8443", false)
	if err != nil {
		t.Fatalf("ParseTargetAddress returned error: %v", err)
	}
	if got.Host != "xn--bcher-kva.example" || got.Port != 8443 || got.Normalized != "xn--bcher-kva.example:8443" {
		t.Fatalf("ParseTargetAddress returned %#v", got)
	}
	if got.IP.IsValid() {
		t.Fatalf("DNS target unexpectedly returned IP %s", got.IP)
	}
}

func TestParseTargetAddressRejectsMalformedTargets(t *testing.T) {
	inputs := []string{
		"",
		"example.com",
		"example.com:0",
		"example.com:65536",
		"example.com:+80",
		"exa mple.com:80",
		"-example.com:80",
		"example-.com:80",
		"example..com:80",
		"example_com:80",
		"[example.com]:80",
		"[192.0.2.1]:80",
		"2001:db8::1:80",
		"[2001:db8::1:80",
		"[2001:db8::1]]:80",
	}
	for _, input := range inputs {
		if _, err := ParseTargetAddress(input, false); err == nil {
			t.Fatalf("target %q must fail", input)
		}
	}
}
