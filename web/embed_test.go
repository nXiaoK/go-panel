package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestEmbeddedSPAResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, "report-only")

	hasIndex := false
	if sub, err := fs.Sub(distFS, "dist"); err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			_ = f.Close()
			hasIndex = true
		}
	}

	tests := []struct {
		path        string
		status      int
		contentType string
		needIndex   bool
	}{
		{path: "/", status: http.StatusOK, contentType: "text/html", needIndex: true},
		{path: "/settings/profile", status: http.StatusOK, contentType: "text/html", needIndex: true},
		{path: "/api/missing", status: http.StatusNotFound, contentType: "application/json"},
		{path: "/assets/missing.js", status: http.StatusNotFound, contentType: "text/plain"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			if tc.needIndex && !hasIndex {
				t.Skip("web/dist/index.html not embedded; run vite build + scripts/sync-web-dist.sh for full SPA coverage")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("content-type=%q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestEmbeddedSPAMissingIndexMessage(t *testing.T) {
	// When SPA assets are not built into the binary, the fallback path returns a
	// clear operator-facing 404 (not a silent empty body).
	if sub, err := fs.Sub(distFS, "dist"); err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			_ = f.Close()
			t.Skip("index.html is embedded in this build")
		}
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, "report-only")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "前端资源未构建") {
		t.Fatalf("body=%q, want missing-build guidance", w.Body.String())
	}
}

func TestStaticAssetNegotiationAndCaching(t *testing.T) {
	original := []byte("console.log('hashed asset')")
	var gz bytes.Buffer
	writer := gzip.NewWriter(&gz)
	if _, err := writer.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	// Brotli 文件内容使用固定字节，测试协商返回的确切文件；Node 测试额外验证 Brotli 解码。
	br := []byte{1, 2, 3, 4}
	files := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>")},
		"assets/app-123.js":    {Data: original},
		"assets/app-123.js.gz": {Data: gz.Bytes()},
		"assets/app-123.js.br": {Data: br},
		"assets/style-123.css": {Data: []byte("body {}")},
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Header("Vary", "Origin"); c.Next() })
	registerFS(router, files, "report-only")
	for _, tc := range []struct {
		accept, encoding string
		body             []byte
	}{
		{"br", "br", br}, {"gzip", "gzip", gz.Bytes()}, {"", "", original},
		{"gzip, br", "br", br}, {"br;q=0, gzip;q=1", "gzip", gz.Bytes()},
		{"br;q=0.5, gzip;q=0.9", "gzip", gz.Bytes()}, {"br;q=0, gzip;q=0", "", original},
		{"*;q=1, br;q=0", "gzip", gz.Bytes()}, {"br;q=NaN", "", original},
	} {
		t.Run(tc.accept, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != http.StatusOK || !bytes.Equal(out.Body.Bytes(), tc.body) {
				t.Fatalf("response=%d %q", out.Code, out.Body.Bytes())
			}
			if got := out.Header().Get("Content-Encoding"); got != tc.encoding {
				t.Fatalf("encoding=%q want %q", got, tc.encoding)
			}
			if !strings.HasPrefix(out.Header().Get("Content-Type"), "text/javascript") {
				t.Fatalf("mime=%q", out.Header().Get("Content-Type"))
			}
			if out.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatal(out.Header())
			}
			vary := strings.Join(out.Header().Values("Vary"), ",")
			if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Encoding") {
				t.Fatalf("Vary=%q", vary)
			}
			if tc.encoding == "gzip" {
				decoded, err := gzip.NewReader(bytes.NewReader(out.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(decoded)
				if err != nil {
					t.Fatal(err)
				}
				_ = decoded.Close()
				if !bytes.Equal(body, original) {
					t.Fatal("gzip body differs")
				}
			}
		})
	}
	for _, path := range []string{"/", "/settings/profile", "/index.html"} {
		out := httptest.NewRecorder()
		router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
		if out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s cache=%q", path, out.Header().Get("Cache-Control"))
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/assets/style-123.css", nil)
	req.Header.Set("Accept-Encoding", "br,gzip")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != 200 || out.Header().Get("Content-Encoding") != "" || out.Body.String() != "body {}" || !strings.HasPrefix(out.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("fallback=%d %s %s", out.Code, out.Header(), out.Body.String())
	}
	// 已存在的旁路文件也不能被直接下载，否则会以 octet-stream 加一年 immutable 缓存返回压缩字节。
	for _, path := range []string{"/assets/missing.js", "/assets/missing.js.gz", "/assets/app-123.js.br", "/assets/app-123.js.gz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "br,gzip")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != 404 || strings.Contains(out.Body.String(), "<!doctype") {
			t.Fatalf("missing fallback: %d %q", out.Code, out.Body.String())
		}
	}
}

func TestHTMLContentSecurityPolicyModesAndResponseScope(t *testing.T) {
	files := fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><html><body>panel</body></html>")},
		"assets/app.js":     {Data: []byte("console.log('panel')")},
		"assets/app.css":    {Data: []byte("body {}")},
		"assets/font.woff2": {Data: []byte("font")},
		"favicon.ico":       {Data: []byte("icon")},
	}
	const expectedPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'; report-uri /api/v1/csp-report; report-to csp-endpoint"
	for _, mode := range []string{"", "report-only", "enforce", "off"} {
		t.Run("mode="+mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			registerFS(router, files, mode)
			for _, path := range []string{"/", "/index.html", "/node", "/settings/profile", "/api/missing", "/api/v1/sub/render/test", "/flow/missing", "/system-info", "/assets/app.js", "/assets/app.css", "/assets/font.woff2", "/favicon.ico", "/assets/missing.js", "/missing.html"} {
				t.Run(path, func(t *testing.T) {
					out := httptest.NewRecorder()
					router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
					isHTML := path == "/" || path == "/index.html" || path == "/node" || path == "/settings/profile"
					for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
						want := ""
						if isHTML && ((mode == "enforce" && name == "Content-Security-Policy") || ((mode == "" || mode == "report-only") && name == "Content-Security-Policy-Report-Only")) {
							want = expectedPolicy
						}
						if got := out.Header().Get(name); got != want {
							t.Fatalf("%s=%q want %q", name, got, want)
						}
					}
					// 报告端点只随启用的 CSP 一起下发，off 模式和非 HTML 响应不得出现。
					wantEndpoints := ""
					if isHTML && mode != "off" {
						wantEndpoints = `csp-endpoint="/api/v1/csp-report"`
					}
					if got := out.Header().Get("Reporting-Endpoints"); got != wantEndpoints {
						t.Fatalf("Reporting-Endpoints=%q want %q", got, wantEndpoints)
					}
					if isHTML && (out.Code != http.StatusOK || out.Body.String() != string(files["index.html"].Data) || !strings.HasPrefix(out.Header().Get("Content-Type"), "text/html")) {
						t.Fatalf("HTML response=%d %s %q", out.Code, out.Header(), out.Body.String())
					}
				})
			}
		})
	}
	t.Run("missing index has no CSP", func(t *testing.T) {
		router := gin.New()
		registerFS(router, fstest.MapFS{}, "enforce")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/node", nil))
		if out.Code != http.StatusNotFound || out.Header().Get("Content-Security-Policy") != "" || out.Header().Get("Content-Security-Policy-Report-Only") != "" {
			t.Fatalf("response=%d %s", out.Code, out.Header())
		}
	})
}
