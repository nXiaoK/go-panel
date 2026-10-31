package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

const proxyBackupHandlerKey = "backup-handler-test-key"

func proxyBackupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	if res := service.UpdateSubscriptionAPIKey(proxyBackupHandlerKey); res.Code != 0 {
		t.Fatal(res)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)
	return router
}

type unreadBackupBody struct{ read bool }

func (b *unreadBackupBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*unreadBackupBody) Close() error               { return nil }

func TestProxyNodeBackupAuthenticatesBeforeReadingBody(t *testing.T) {
	router := proxyBackupRouter(t)
	for _, route := range []string{"backup", "restore", "backups", "restore-reported", "reported-restores"} {
		body := &unreadBackupBody{}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sub/node/"+route, nil)
		req.Body = body
		req.ContentLength = service.MaxProxyNodeBackupSize + 1
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != http.StatusUnauthorized || body.read {
			t.Fatalf("%s read unauthenticated snapshot before auth: status=%d read=%v", route, out.Code, body.read)
		}
		if out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store", route)
		}
	}
}

func TestProxyNodeBackupHTTPRoundTripAndSizeLimits(t *testing.T) {
	router := proxyBackupRouter(t)
	request := func(route string, body []byte, chunked bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sub/node/"+route, bytes.NewReader(body))
		if chunked {
			req.ContentLength = -1
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", proxyBackupHandlerKey)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	backup := dto.ProxyNodeBackupDto{
		Version: 1, SourceHost: "node-handler-before-reinstall", Server: "192.0.2.17",
		Database: json.RawMessage(`{"version":"4.0.0","xray":{"vless":{"port":443,"uuid":"original-uuid","private_key":"secret-reality-private-key","public_key":"original-public-key","short_id":"abcdef12","sni":"example.com"}},"singbox":{}}`),
		// 超过普通 API 的 1 MiB，用于验证快照上传确实经过独立 4 MiB 限制。
		Files: map[string]string{"certs/server.key": strings.Repeat("x", (1<<20)+1024)},
	}
	raw, _ := json.Marshal(backup)
	out := request("backup", raw, false)
	var res result.R
	if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &res) != nil || res.Code != 0 {
		t.Fatalf("valid large backup rejected: %d %s", out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "secret-reality") {
		t.Fatal("upload response leaked private key")
	}
	for _, chunked := range []bool{false, true} {
		out = request("backup", bytes.Repeat([]byte("x"), service.MaxProxyNodeBackupSize+1), chunked)
		if out.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized backup accepted (chunked=%v): %d", chunked, out.Code)
		}
	}
	out = request("restore", []byte(`{"sourceHost":"node-handler-before-reinstall"}`), false)
	var restored struct {
		Code int                    `json:"code"`
		Data dto.ProxyNodeBackupDto `json:"data"`
	}
	if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &restored) != nil || restored.Code != 0 {
		t.Fatalf("restore failed: %d %s", out.Code, out.Body.String())
	}
	if !bytes.Equal(restored.Data.Database, backup.Database) || restored.Data.Files["certs/server.key"] != backup.Files["certs/server.key"] {
		t.Fatal("restore failed to retain original credentials after rejected upload")
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private restore response can be cached")
	}
	out = request("backups", []byte(`{}`), false)
	if out.Code != http.StatusOK || strings.Contains(out.Body.String(), "secret-reality") || strings.Contains(out.Body.String(), "database") {
		t.Fatalf("backup list leaked snapshot: %d %s", out.Code, out.Body.String())
	}
}

func TestReportedNodeRestoreHTTPEnvelope(t *testing.T) {
	router := proxyBackupRouter(t)
	node := model.ProxyNode{ExternalID: "node-http-reinstalled-socks", Name: "旧 SOCKS5", Protocol: "socks5", Server: "192.0.2.17", Port: 21001, Username: "original-user", Password: "original-password", Options: `{"source":"vless-server.sh","sourceHost":"node-http-reinstalled","originalProtocol":"socks","originalPort":"21001"}`}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	request := func(route, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sub/node/"+route, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", proxyBackupHandlerKey)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	out := request("reported-restores", `{}`)
	if out.Code != http.StatusOK || strings.Contains(out.Body.String(), "original-password") || !strings.Contains(out.Body.String(), "--flux-panel-restore-reported") {
		t.Fatalf("bad metadata response: %d %s", out.Code, out.Body.String())
	}
	out = request("restore-reported", `{"sourceHost":"node-http-reinstalled"}`)
	var response struct {
		Code int                               `json:"code"`
		Data service.ReportedNodeRestoreResult `json:"data"`
	}
	if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &response) != nil || response.Code != 0 {
		t.Fatalf("bad restore envelope: %d %s", out.Code, out.Body.String())
	}
	if response.Data.Snapshot.SourceHost != "node-http-reinstalled" || len(response.Data.Notices) == 0 || !strings.Contains(string(response.Data.Snapshot.Database), "original-password") {
		t.Fatalf("missing snapshot or notices: %#v", response)
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("reconstructed credentials response cacheable")
	}
	var count int64
	model.DB.Model(&model.ProxyNodeBackup{}).Count(&count)
	if count != 0 {
		t.Fatal("HTTP restore persisted draft snapshot")
	}
}
