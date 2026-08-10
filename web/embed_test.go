package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEmbeddedSPAResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)

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
	Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "前端资源未构建") {
		t.Fatalf("body=%q, want missing-build guidance", w.Body.String())
	}
}
