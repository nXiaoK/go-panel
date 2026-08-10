package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func publicHTTPTestServer(t *testing.T, handler http.Handler) (string, *http.Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("parse test server URL: %v", err)
	}
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	client.Transport = transport
	return "http://node-assets.example:" + serverURL.Port(), client, server.Close
}

func TestSameNodeDownloadOriginNormalizesHostnameAndDefaultPort(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return u
	}
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{from: "https://Panel.Example/asset", to: "https://panel.example:443/next", want: true},
		{from: "http://panel.example/asset", to: "http://PANEL.EXAMPLE:80/next", want: true},
		{from: "https://panel.example:444/asset", to: "https://panel.example:443/next", want: false},
		{from: "https://panel.example/asset", to: "http://panel.example:80/next", want: false},
		{from: "https://panel.example/asset", to: "https://user:password@panel.example/next", want: false},
	}
	for _, tc := range tests {
		if got := sameNodeDownloadOrigin(parse(tc.from), parse(tc.to)); got != tc.want {
			t.Fatalf("sameNodeDownloadOrigin(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestDownloadNodeFileRejectsHTTPByDefault(t *testing.T) {
	rawURL, client, closeServer := publicHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer closeServer()

	err := downloadNodeFileWithClient(
		context.Background(),
		client,
		rawURL,
		filepath.Join(t.TempDir(), "agent"),
		false,
	)
	if err == nil {
		t.Fatal("HTTP download must fail by default")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("HTTP rejection error = %q, want HTTPS policy error", err)
	}
}

func TestDownloadNodeFileRejectsEmptyHostname(t *testing.T) {
	err := downloadNodeFileWithClient(
		context.Background(),
		nil,
		"https://:6365/asset",
		filepath.Join(t.TempDir(), "agent"),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("empty hostname error = %v, want invalid URL", err)
	}
}

func TestDownloadNodeFileRejectsUserinfoBeforeNetwork(t *testing.T) {
	err := downloadNodeFileWithClient(
		context.Background(),
		nil,
		"https://user:password@127.0.0.1:1/asset",
		filepath.Join(t.TempDir(), "agent"),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("userinfo URL error = %v, want invalid URL", err)
	}
}

func TestDownloadNodeFileAllowsLoopbackHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("loopback-binary"))
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "agent")
	if err := downloadNodeFileWithClient(context.Background(), server.Client(), server.URL, target, false); err != nil {
		t.Fatalf("loopback HTTP download failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read downloaded target: %v", err)
	}
	if string(got) != "loopback-binary" {
		t.Fatalf("downloaded target = %q, want loopback-binary", got)
	}
}

func TestDownloadNodeFileAllowsExplicitInsecureHTTP(t *testing.T) {
	rawURL, client, closeServer := publicHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("explicit-insecure"))
	}))
	defer closeServer()

	target := filepath.Join(t.TempDir(), "agent")
	if err := downloadNodeFileWithClient(context.Background(), client, rawURL, target, true); err != nil {
		t.Fatalf("explicit insecure HTTP download failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read downloaded target: %v", err)
	}
	if string(got) != "explicit-insecure" {
		t.Fatalf("downloaded target = %q, want explicit-insecure", got)
	}
}

func TestDownloadNodeFileClonesClientAndHonorsCallerRedirectPolicy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/asset", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer server.Close()

	client := server.Client()
	originalCalls := 0
	originalErr := errors.New("original redirect callback")
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		originalCalls++
		return originalErr
	}
	target := filepath.Join(t.TempDir(), "agent")
	err := downloadNodeFileWithClient(context.Background(), client, server.URL+"/start", target, false)
	if !errors.Is(err, originalErr) {
		t.Fatalf("redirect error = %v, want caller policy error", err)
	}
	if originalCalls != 1 {
		t.Fatalf("caller redirect callback invoked %d times, want 1", originalCalls)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, originalErr) {
		t.Fatalf("caller redirect callback was replaced: %v", err)
	}
}

func TestDownloadNodeFileRejectsCrossOriginRedirect(t *testing.T) {
	targetServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer targetServer.Close()
	sourceServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL, http.StatusFound)
	}))
	defer sourceServer.Close()

	client := sourceServer.Client()
	callerChecks := 0
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		callerChecks++
		return nil
	}
	err := downloadNodeFileWithClient(
		context.Background(), client, sourceServer.URL, filepath.Join(t.TempDir(), "agent"), false,
	)
	if err == nil || (!strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "origin")) {
		t.Fatalf("cross-origin redirect error = %v, want redirect policy rejection", err)
	}
	if callerChecks != 0 {
		t.Fatalf("unsafe redirect reached caller callback %d times", callerChecks)
	}
}

func TestDownloadNodeFileHonorsCallerErrUseLastResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/asset", http.StatusFound)
	}))
	defer server.Close()

	client := server.Client()
	calls := 0
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		calls++
		return http.ErrUseLastResponse
	}
	err := downloadNodeFileWithClient(
		context.Background(), client, server.URL, filepath.Join(t.TempDir(), "agent"), false,
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("ErrUseLastResponse result = %v, want redirect response status rejection", err)
	}
	if calls != 1 {
		t.Fatalf("caller redirect callback calls = %d, want 1", calls)
	}
}

func TestDownloadNodeFileKeepsDefaultTenRedirectLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		if step < 11 {
			http.Redirect(w, r, fmt.Sprintf("/%d", step+1), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer server.Close()

	err := downloadNodeFileWithClient(
		context.Background(), server.Client(), server.URL+"/0", filepath.Join(t.TempDir(), "agent"), false,
	)
	if err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("redirect limit error = %v, want default 10-redirect rejection", err)
	}
}

func TestDownloadNodeFileRejectsHTTPSDowngradeRedirect(t *testing.T) {
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer targetServer.Close()
	sourceServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL, http.StatusFound)
	}))
	defer sourceServer.Close()

	err := downloadNodeFileWithClient(
		context.Background(), sourceServer.Client(), sourceServer.URL, filepath.Join(t.TempDir(), "agent"), true,
	)
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("HTTPS downgrade redirect error = %v, want redirect rejection", err)
	}
}

func TestDownloadNodeFileRequiresFinalStatusOK(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := downloadNodeFileWithClient(
		context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "agent"), false,
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("non-200 response error = %v, want HTTP 503", err)
	}
}

func TestDownloadNodeFileRejectsWrongContentType(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("binary"))
	}))
	defer server.Close()

	err := downloadNodeFileWithClient(
		context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "agent"), false,
	)
	if err == nil || !strings.Contains(err.Error(), "Content-Type") {
		t.Fatalf("wrong content type error = %v, want Content-Type rejection", err)
	}
}

func TestDownloadNodeFileAcceptsMissingContentType(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := downloadNodeFileWithClient(
		context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "agent"), false,
	); err != nil {
		t.Fatalf("missing Content-Type download failed: %v", err)
	}
}

func TestDownloadNodeFileAcceptsExecutableContentTypeParameters(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-executable; charset=binary")
		_, _ = w.Write([]byte("binary"))
	}))
	defer server.Close()

	if err := downloadNodeFileWithClient(
		context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "agent"), false,
	); err != nil {
		t.Fatalf("executable media type download failed: %v", err)
	}
}

func TestDownloadNodeFileRejects129MiBResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.CopyN(w, zeroReader{}, 129<<20)
	}))
	defer server.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "agent")
	if err := os.WriteFile(target, []byte("old-agent"), 0700); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	err := downloadNodeFileWithClient(context.Background(), server.Client(), server.URL, target, false)
	if err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Fatalf("oversized response error = %v, want 128 MiB rejection", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read preserved target: %v", readErr)
	}
	if string(got) != "old-agent" {
		t.Fatalf("target after oversized response = %q, want old-agent", got)
	}
}

func TestDownloadNodeFileInterruptedPreservesTarget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write([]byte("partial"))
	}))
	defer server.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "agent")
	if err := os.WriteFile(target, []byte("old-agent"), 0700); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	if err := downloadNodeFileWithClient(context.Background(), server.Client(), server.URL, target, false); err == nil {
		t.Fatal("interrupted download returned nil error")
	} else if !strings.Contains(err.Error(), "write") {
		t.Fatalf("interrupted download error = %v, want write failure", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read preserved target: %v", err)
	}
	if string(got) != "old-agent" {
		t.Fatalf("target after interrupted response = %q, want old-agent", got)
	}
}

func TestDownloadNodeFileStagesAndInstallsValidAsset(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("new-agent"))
	}))
	defer server.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "agent")
	if err := os.WriteFile(target, []byte("old-agent"), 0600); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	if err := downloadNodeFileWithClient(context.Background(), server.Client(), server.URL, target, false); err != nil {
		t.Fatalf("valid download failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed target: %v", err)
	}
	if string(got) != "new-agent" {
		t.Fatalf("installed target = %q, want new-agent", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat installed target: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("installed mode = %04o, want 0700", info.Mode().Perm())
	}
	staged, err := filepath.Glob(filepath.Join(dir, ".agent.upgrade-*"))
	if err != nil {
		t.Fatalf("glob staged files: %v", err)
	}
	if len(staged) != 0 {
		t.Fatalf("staged files remain after install: %v", staged)
	}
}
