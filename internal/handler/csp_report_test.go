package handler

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/middleware"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(previous); log.SetFlags(flags) })
	return &buf
}

func TestCSPReportEndpointIsPublicAndLogsSanitizedViolations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)
	logs := captureLog(t)
	legacy := `{"csp-report":{"document-uri":"https://panel.example/node?token=secret#x","effective-directive":"script-src-elem","blocked-uri":"https://evil.example/x.js?k=v","disposition":"report"}}`
	req := httptest.NewRequest(http.MethodPost, middleware.CSPReportPath, strings.NewReader(legacy))
	req.Header.Set("Content-Type", "application/csp-report")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != http.StatusNoContent || out.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("response=%d %v", out.Code, out.Header())
	}
	got := logs.String()
	if !strings.Contains(got, `directive="script-src-elem"`) || !strings.Contains(got, `blocked="https://evil.example"`) || !strings.Contains(got, `document="https://panel.example"`) {
		t.Fatalf("log=%q", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "k=v") {
		t.Fatalf("query string leaked into log: %q", got)
	}
}

func TestParseCSPReportingAPIBatch(t *testing.T) {
	var entries []string
	for index := 0; index < 8; index++ {
		entries = append(entries, fmt.Sprintf(`{"type":"csp-violation","url":"https://panel.example/%d","body":{"effectiveDirective":"img-src","blockedURL":"https://cdn.example/%d.png","disposition":"enforce"}}`, index, index))
	}
	entries = append([]string{`{"type":"deprecation","body":{}}`}, entries...)
	violations := parseCSPReport([]byte("[" + strings.Join(entries, ",") + "]"))
	if len(violations) != cspReportMaxEntries {
		t.Fatalf("violations=%d, want %d", len(violations), cspReportMaxEntries)
	}
	if violations[0].Directive != "img-src" || violations[0].DocumentURL != "https://panel.example/0" || violations[0].Disposition != "enforce" {
		t.Fatalf("first=%+v", violations[0])
	}
	if parseCSPReport([]byte("not json")) != nil {
		t.Fatal("invalid JSON produced violations")
	}
}

func TestSanitizeCSPField(t *testing.T) {
	for input, want := range map[string]string{
		"script-src-elem": "script-src-elem",
		"script-src https://user:secret@example.com/path?token=secret": "script-src",
		"inline\n伪造日志\x1b[31m":                                         "inline",
		"https://user:secret@example.com":                              "[invalid]",
		"report":                                                       "report",
	} {
		if got := sanitizeCSPField(input); got != want {
			t.Errorf("field %q=%q, want %q", input, got, want)
		}
	}
	if got := []rune(sanitizeCSPField(strings.Repeat("a", 500))); len(got) != cspReportMaxField+1 {
		t.Fatalf("length=%d", len(got))
	}
}

func TestSanitizeCSPURLNeverLogsCredentialsOrPayloads(t *testing.T) {
	for input, want := range map[string]string{
		"https://user:secret@panel.example:8443/api/v1/sub/render/secret?token=secret#secret": "https://panel.example:8443",
		"/node?token=secret#secret":                         "[relative URL]",
		"//user:secret@panel.example/node?token=secret":     "[relative URL]",
		"https://panel.example/invalid%zz?token=secret":     "[invalid URL]",
		"https://user:secret@panel.example/\n?token=secret": "[invalid URL]",
		"data:text/html,secret":                             "data:",
		"blob:https://panel.example/secret":                 "blob:",
		"https:secret":                                      "[invalid URL]",
		"inline":                                            "inline",
		"eval":                                              "eval",
		"wss://[::1]:8443/path?token=secret":                "wss://[::1]:8443",
	} {
		t.Run(input, func(t *testing.T) {
			if got := sanitizeCSPURL(input); got != want || strings.Contains(got, "secret") {
				t.Fatalf("sanitized=%q, want %q", got, want)
			}
		})
	}
}

func TestCSPReportLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newCSPReportLimiter(func() time.Time { return now })
	for index := 0; index < cspReportPerClientLimit; index++ {
		if !limiter.allow("198.51.100.1") {
			t.Fatalf("request %d rejected", index)
		}
	}
	if limiter.allow("198.51.100.1") {
		t.Fatal("per-client limit not enforced")
	}
	allowed := cspReportPerClientLimit
	for client := 0; allowed < cspReportGlobalLimit; client++ {
		if limiter.allow(fmt.Sprintf("203.0.113.%d", client)) {
			allowed++
		}
	}
	if limiter.allow("192.0.2.200") {
		t.Fatal("global limit not enforced")
	}
	now = now.Add(cspReportWindow)
	if !limiter.allow("198.51.100.1") {
		t.Fatal("window did not reset")
	}
}

func TestCSPReportLimiterBoundsTrackedClients(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newCSPReportLimiter(func() time.Time { return now })
	for client := 0; client < cspReportGlobalLimit*2; client++ {
		limiter.allow(fmt.Sprint(client))
	}
	if len(limiter.clients) != cspReportGlobalLimit {
		t.Fatalf("tracked clients=%d", len(limiter.clients))
	}
	now = now.Add(cspReportWindow)
	if !limiter.allow("new-client") || len(limiter.clients) != 1 {
		t.Fatalf("expired clients not cleared: %v", limiter.clients)
	}
}

func TestCSPReportRejectsOversizedBodiesWithoutLogging(t *testing.T) {
	logs := captureLog(t)
	gin.SetMode(gin.TestMode)
	limiter := newCSPReportLimiter(time.Now)
	body := `{"csp-report":{"effective-directive":"script-src","blocked-uri":"` + strings.Repeat("a", cspReportMaxBody) + `"}}`
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest(http.MethodPost, middleware.CSPReportPath, strings.NewReader(body))
	handleCSPReport(c, limiter)
	c.Writer.WriteHeaderNow()
	if out.Code != http.StatusNoContent || logs.Len() != 0 || limiter.global != 0 {
		t.Fatalf("code=%d log=%q global=%d", out.Code, logs.String(), limiter.global)
	}
}
