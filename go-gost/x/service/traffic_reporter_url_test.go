package service

import (
	"net/url"
	"testing"
)

func TestSetHTTPReportURLUsesConfiguredHTTPSBase(t *testing.T) {
	if err := SetHTTPReportURL("https://panel.example.com/base/", "node secret"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(httpReportURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "panel.example.com" || u.Path != "/base/flow/upload" {
		t.Fatalf("traffic report URL = %q", httpReportURL)
	}
	if u.Query().Get("secret") != "node secret" {
		t.Fatalf("secret query = %q", u.RawQuery)
	}
}
