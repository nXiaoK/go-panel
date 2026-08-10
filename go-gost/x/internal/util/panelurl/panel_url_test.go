package panelurl

import (
	"net/url"
	"testing"
)

func TestBuildWebSocketURLPreservesBasePathAndReportsPanelURL(t *testing.T) {
	values := url.Values{}
	values.Set("secret", "node secret")
	values.Set("panelUrl", "https://panel.example.com/base")
	got, err := BuildWebSocketURL("https://panel.example.com/base/", values)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wss" || u.Host != "panel.example.com" || u.Path != "/base/system-info" {
		t.Fatalf("WebSocket URL = %q", got)
	}
	if u.Query().Get("secret") != "node secret" || u.Query().Get("panelUrl") != "https://panel.example.com/base" {
		t.Fatalf("query = %q", u.RawQuery)
	}
}

func TestNormalizeBaseKeepsLegacyBareHost(t *testing.T) {
	tests := map[string]string{
		"panel.example.com:6365/": "http://panel.example.com:6365",
		"[2001:db8::1]:6365/":     "http://[2001:db8::1]:6365",
	}
	for raw, want := range tests {
		got, err := NormalizeBase(raw)
		if err != nil {
			t.Fatalf("NormalizeBase(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("NormalizeBase(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeBaseRejectsAmbiguousURLs(t *testing.T) {
	for _, raw := range []string{
		"ftp://panel.example.com",
		"https://user:pass@panel.example.com",
		"https://panel.example.com?secret=value",
		"https://panel.example.com?",
		"https://panel.example.com/#fragment",
		"https://panel.example.com/%2e%2e/assets",
		"https://panel.example.com/base//nested",
	} {
		if _, err := NormalizeBase(raw); err == nil {
			t.Fatalf("NormalizeBase(%q) unexpectedly succeeded", raw)
		}
	}
}
