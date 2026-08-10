package socket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReporterWebSocketURLReportsLocalPanelBase(t *testing.T) {
	got, baseURL, err := buildReporterWebSocketURL(
		"https://panel.example.com/base/", "node secret", "1.2.6", 1, 2, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if baseURL != "https://panel.example.com/base" {
		t.Fatalf("base URL = %q", baseURL)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wss" || u.Host != "panel.example.com" || u.Path != "/base/system-info" {
		t.Fatalf("WebSocket URL = %q", got)
	}
	if u.Query().Get("panelUrl") != baseURL || u.Query().Get("secret") != "node secret" {
		t.Fatalf("query = %q", u.RawQuery)
	}
}

func TestSelectGostUpgradeBaseURLPrefersLocalHistory(t *testing.T) {
	got, err := selectGostUpgradeBaseURL(
		"https://historical.example.com/base/",
		"https://new-panel.example.com",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://historical.example.com/base" {
		t.Fatalf("upgrade base URL = %q", got)
	}
}

func TestSelectGostUpgradeBaseURLKeepsHTTPSPolicy(t *testing.T) {
	if _, err := selectGostUpgradeBaseURL("http://historical.example.com", "", false); err == nil {
		t.Fatal("public historical HTTP URL unexpectedly accepted")
	}
	got, err := selectGostUpgradeBaseURL("http://historical.example.com", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://historical.example.com" {
		t.Fatalf("upgrade base URL = %q", got)
	}
}

func TestDownloadFileAllowsSameOriginRedirect(t *testing.T) {
	payload := []byte("gost-binary")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/asset", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dst := filepath.Join(t.TempDir(), "gost")
	if err := downloadFileWithClient(context.Background(), server.Client(), server.URL+"/start", dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload = %q", got)
	}
}

func TestDownloadFileRejectsCrossOriginRedirect(t *testing.T) {
	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("unexpected"))
	}))
	defer assetServer.Close()
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, assetServer.URL+"/asset", http.StatusFound)
	}))
	defer redirectServer.Close()

	err := downloadFileWithClient(
		context.Background(), redirectServer.Client(), redirectServer.URL+"/start", filepath.Join(t.TempDir(), "gost"),
	)
	if err == nil || !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("cross-origin redirect error = %v", err)
	}
}

func TestDownloadFileRejectsHTTPDowngradeRedirect(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("unexpected"))
	}))
	defer httpServer.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpServer.URL+"/asset", http.StatusFound)
	}))
	defer tlsServer.Close()

	err := downloadFileWithClient(
		context.Background(), tlsServer.Client(), tlsServer.URL+"/start", filepath.Join(t.TempDir(), "gost"),
	)
	if err == nil || !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("HTTPS downgrade redirect error = %v", err)
	}
}

func TestDownloadFileRejectsUnexpectedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>not a binary</html>"))
	}))
	defer server.Close()

	err := downloadFileWithClient(
		context.Background(), server.Client(), server.URL+"/asset", filepath.Join(t.TempDir(), "gost"),
	)
	if err == nil || !strings.Contains(err.Error(), "Content-Type") {
		t.Fatalf("unexpected Content-Type error = %v", err)
	}
}
