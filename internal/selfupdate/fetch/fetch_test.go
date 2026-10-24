package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const assetURL = "https://github.com/nXiaoK/go-panel/releases/download/v0.3.0/go-panel-v0.3.0-linux-amd64.tar.gz"

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func response(req *http.Request, status int, body io.Reader, length int64) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(body), ContentLength: length, Request: req}
}

func redirect(req *http.Request, location string) *http.Response {
	resp := response(req, http.StatusFound, strings.NewReader(""), 0)
	resp.Header.Set("Location", location)
	return resp
}

func clientWith(fn transportFunc) *Client {
	return &Client{HTTP: &http.Client{Transport: fn}, StallTimeout: 200 * time.Millisecond}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestURLAppliesProxyAndRejectsOtherOrigins(t *testing.T) {
	direct := &Client{}
	if got, err := direct.URL(assetURL); err != nil || got != assetURL {
		t.Fatalf("direct URL=%q err=%v", got, err)
	}
	proxied := &Client{ProxyPrefix: "https://ghproxy.example/mirror/"}
	if got, err := proxied.URL(assetURL); err != nil || got != "https://ghproxy.example/mirror/"+assetURL {
		t.Fatalf("proxied URL=%q err=%v", got, err)
	}
	for _, upstream := range []string{
		"http://github.com/x", "https://evil.example/x", "https://github.com:8443/x",
		"https://user@github.com/x", "https://api.github.com/x", "ftp://github.com/x",
	} {
		if _, err := direct.URL(upstream); err == nil {
			t.Fatalf("URL(%q) must be rejected", upstream)
		}
	}
	for _, proxy := range []string{"http://proxy.example", "https://u:p@proxy.example", "https://proxy.example/?q=1"} {
		if _, err := (&Client{ProxyPrefix: proxy}).URL(assetURL); err == nil {
			t.Fatalf("proxy %q must be rejected", proxy)
		}
	}
}

func TestRedirectPolicy(t *testing.T) {
	payload := []byte("archive")
	target := func(location string) *Client {
		return clientWith(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "github.com" {
				return redirect(req, location), nil
			}
			return response(req, http.StatusOK, bytes.NewReader(payload), int64(len(payload))), nil
		})
	}
	var out bytes.Buffer
	if err := target("https://release-assets.githubusercontent.com/asset?sig=1").Download(context.Background(), assetURL, &out, int64(len(payload)), digest(payload), nil); err != nil {
		t.Fatalf("redirect to the GitHub CDN must succeed: %v", err)
	}
	for _, location := range []string{"http://release-assets.githubusercontent.com/a", "https://169.254.169.254/latest", "https://evil.example/a"} {
		if err := target(location).Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); err == nil {
			t.Fatalf("redirect to %q must be rejected", location)
		}
	}
	loops := clientWith(func(req *http.Request) (*http.Response, error) {
		return redirect(req, assetURL), nil
	})
	if _, err := loops.FetchSmall(context.Background(), assetURL, 100); err == nil {
		t.Fatal("redirect loops must stop after the limit")
	}
	proxied := target("https://ghproxy.example/cached")
	proxied.ProxyPrefix = "https://ghproxy.example"
	proxied.HTTP.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/cached" {
			return response(req, http.StatusOK, bytes.NewReader(payload), int64(len(payload))), nil
		}
		if !strings.HasPrefix(req.URL.String(), "https://ghproxy.example/https://github.com/") {
			t.Errorf("proxied request went to %s", req.URL)
		}
		return redirect(req, "https://ghproxy.example/cached"), nil
	})
	if err := proxied.Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); err != nil {
		t.Fatalf("redirects within the configured proxy host must be allowed: %v", err)
	}
}

func TestFetchSmallEnforcesLimitAndStatus(t *testing.T) {
	client := clientWith(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/missing"):
			return response(req, http.StatusNotFound, strings.NewReader("nope"), 4), nil
		case strings.HasSuffix(req.URL.Path, "/large"):
			return response(req, http.StatusOK, strings.NewReader(strings.Repeat("x", 101)), -1), nil
		default:
			return response(req, http.StatusOK, strings.NewReader("ok"), 2), nil
		}
	})
	if data, err := client.FetchSmall(context.Background(), "https://github.com/o/r/small", 100); err != nil || string(data) != "ok" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := client.FetchSmall(context.Background(), "https://github.com/o/r/missing", 100); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404 err=%v", err)
	}
	if _, err := client.FetchSmall(context.Background(), "https://github.com/o/r/large", 100); err == nil {
		t.Fatal("responses over the limit must fail")
	}
}

func TestDownloadVerifiesSizeAndHash(t *testing.T) {
	payload := bytes.Repeat([]byte("go-panel"), 300_000)
	serve := func(body []byte, length int64) *Client {
		return clientWith(func(req *http.Request) (*http.Response, error) {
			return response(req, http.StatusOK, bytes.NewReader(body), length), nil
		})
	}
	var out bytes.Buffer
	var mu sync.Mutex
	var calls [][2]int64
	err := serve(payload, int64(len(payload))).Download(context.Background(), assetURL, &out, int64(len(payload)), digest(payload), func(done, total int64) {
		mu.Lock()
		calls = append(calls, [2]int64{done, total})
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Fatal("downloaded bytes differ from the payload")
	}
	if last := calls[len(calls)-1]; last != [2]int64{int64(len(payload)), int64(len(payload))} {
		t.Fatalf("final progress=%v", last)
	}

	tampered := append([]byte{}, payload...)
	tampered[10] ^= 0xff
	if err := serve(tampered, int64(len(tampered))).Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("tampered err=%v", err)
	}
	if err := serve(append(payload, 'x'), -1).Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("oversized err=%v", err)
	}
	if err := serve(payload[:100], -1).Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("truncated err=%v", err)
	}
	if err := serve(payload, int64(len(payload))+1).Download(context.Background(), assetURL, io.Discard, int64(len(payload)), digest(payload), nil); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("declared length mismatch err=%v", err)
	}
}

// stallingBody 先返回部分数据，然后像真实连接一样阻塞到请求被取消。
type stallingBody struct {
	ctx  context.Context
	sent bool
}

func (b *stallingBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial"), nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func TestDownloadDetectsStallAndCancellation(t *testing.T) {
	client := clientWith(func(req *http.Request) (*http.Response, error) {
		return response(req, http.StatusOK, &stallingBody{ctx: req.Context()}, 1000), nil
	})
	started := time.Now()
	err := client.Download(context.Background(), assetURL, io.Discard, 1000, strings.Repeat("0", 64), nil)
	if !errors.Is(err, ErrStalled) || time.Since(started) > 5*time.Second {
		t.Fatalf("stall err=%v after %s", err, time.Since(started))
	}

	client.StallTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err = client.Download(ctx, assetURL, io.Discard, 1000, strings.Repeat("0", 64), nil)
	if err == nil || errors.Is(err, ErrStalled) {
		t.Fatalf("cancellation err=%v, want a non-stall error", err)
	}
	if err := client.Download(context.Background(), assetURL, io.Discard, 0, "", nil); err == nil {
		t.Fatal("a zero expected size must be rejected")
	}
}

func TestFreeBytesReportsSpace(t *testing.T) {
	free, err := FreeBytes(t.TempDir())
	if err != nil || free == 0 {
		t.Fatalf("free=%d err=%v", free, err)
	}
	if _, err := FreeBytes("/definitely/missing/" + strconv.Itoa(time.Now().Nanosecond())); err == nil {
		t.Fatal("a missing directory must fail")
	}
}
