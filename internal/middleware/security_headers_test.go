package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/handler"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
	"github.com/nXiaoK/go-panel/internal/ws"
	"github.com/nXiaoK/go-panel/web"
)

func assertSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	} {
		if got := header.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s=%q, want one value %q", name, got, want)
		}
	}
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only", "Strict-Transport-Security"} {
		if got := header.Get(name); got != "" {
			t.Errorf("unexpected phase-one header %s=%q", name, got)
		}
	}
}

func newSecurityHeadersRouter(t *testing.T) *gin.Engine {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router, "https://panel.example.com", false)
	web.Register(router)
	t.Cleanup(func() { ws.SetAllowedOrigins("") })
	return router
}

func TestSecurityHeadersCoverRegisteredResponses(t *testing.T) {
	router := newSecurityHeadersRouter(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/healthz", "", http.StatusOK},
		{http.MethodPost, "/api/v1/user/login", "{}", http.StatusOK},
		{http.MethodPost, "/api/v1/node/list", "{}", http.StatusOK},
		{http.MethodGet, "/api/v1/sub/render/x", "", http.StatusNotFound},
		{http.MethodGet, "/api/v1/missing", "", http.StatusNotFound},
		{http.MethodGet, "/assets/missing.js", "", http.StatusNotFound},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("status=%d, want %d: %s", out.Code, tc.status, out.Body)
			}
			assertSecurityHeaders(t, out.Header())
		})
	}
	t.Run("SPA fallback retains its response", func(t *testing.T) {
		// 使用真实静态服务作对照：没有构建产物时也验证安全头，有产物时同时覆盖 HTML 回退。
		baseline := gin.New()
		web.Register(baseline)
		want, got := httptest.NewRecorder(), httptest.NewRecorder()
		baseline.ServeHTTP(want, httptest.NewRequest(http.MethodGet, "/settings/profile", nil))
		router.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/settings/profile", nil))
		if got.Code != want.Code || got.Body.String() != want.Body.String() || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
			t.Fatalf("SPA response changed: status=%d body=%q", got.Code, got.Body)
		}
		assertSecurityHeaders(t, got.Header())
	})
	for _, path := range []string{"/api/v1/user/login", "/unregistered-preflight"} {
		t.Run("OPTIONS"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			req.Header.Set("Origin", "https://panel.example.com")
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != http.StatusNoContent || out.Body.Len() != 0 {
				t.Fatalf("preflight=%d %q", out.Code, out.Body)
			}
			if out.Header().Get("Access-Control-Allow-Origin") != "https://panel.example.com" {
				t.Fatal("CORS origin was lost")
			}
			assertSecurityHeaders(t, out.Header())
		})
	}
}

func TestSecurityHeadersPreserveSubscriptionFormats(t *testing.T) {
	router := newSecurityHeadersRouter(t)
	profile := model.SubscriptionProfile{Name: "headers", Token: "header-token", Status: 1, DefaultFormat: "surge", SurgeTemplate: "[General]\n[Proxy]\n"}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	node := model.ProxyNode{ExternalID: "headers-vless", Name: "headers-vless", Protocol: "vless", Server: "node.example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111", Security: "tls", Status: 1}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"surge", "clash", "singbox", "v2rayn"} {
		t.Run(format, func(t *testing.T) {
			wantBody, wantType, err := service.RenderSubscription(profile.Token, format)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/sub/render/" + profile.Token + "/" + format, "/api/v1/sub/render/" + profile.Token + "?format=" + format} {
				out := httptest.NewRecorder()
				router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
				if out.Code != http.StatusOK || out.Body.String() != wantBody || out.Header().Get("Content-Type") != wantType {
					t.Fatalf("subscription changed: status=%d type=%q body=%q", out.Code, out.Header().Get("Content-Type"), out.Body)
				}
				assertSecurityHeaders(t, out.Header())
			}
		})
	}
}

func TestSecurityHeadersPreserveWebSocketHandshakeAndAuthorization(t *testing.T) {
	router := newSecurityHeadersRouter(t)
	crypto.InitJwt("headers-websocket-test-secret")
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 0).Error; err != nil {
		t.Fatal(err)
	}
	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{ExpTime: &expires, User: "headers-user", Pwd: "unused", RoleID: 1, Status: model.UserStatusActive}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	userToken, err := crypto.GenerateToken(user.ID, user.User, user.RoleID, 0)
	if err != nil {
		t.Fatal(err)
	}
	node := model.Node{Name: "headers-node", Secret: "headers-node-secret", IP: "127.0.0.1", ServerIP: "127.0.0.1"}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	// 保留真实上线对账，只增加完成信号；关闭连接后等待异步钩子结束，再清理数据库。
	reconciled := make(chan struct{})
	ws.SetNodeConnectedHook(func(nodeID int64) {
		defer close(reconciled)
		service.SyncNodeForwardsOnConnect(nodeID)
	})
	t.Cleanup(func() { ws.SetNodeConnectedHook(service.SyncNodeForwardsOnConnect) })

	finished := make(chan struct{}, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { finished <- struct{}{} }()
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	waitFinished := func() {
		t.Helper()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("WebSocket handler did not finish")
		}
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/system-info"
	adminDialer := websocket.Dialer{Subprotocols: []string{"jwt." + token}}
	admin, adminResponse, err := adminDialer.Dial(wsURL+"?type=0&secret=invalid-query-token", http.Header{"Origin": {"https://panel.example.com"}})
	if err != nil {
		t.Fatalf("admin handshake: %v", err)
	}
	defer func() { _ = admin.Close(); waitFinished() }()
	if admin.Subprotocol() != "jwt."+token {
		t.Fatalf("JWT subprotocol changed: %q", admin.Subprotocol())
	}
	assertSecurityHeaders(t, adminResponse.Header)

	nodeConn, nodeResponse, err := websocket.DefaultDialer.Dial(wsURL+"?type=1&secret="+url.QueryEscape(node.Secret), nil)
	if err != nil {
		t.Fatalf("node handshake: %v", err)
	}
	defer func() {
		_ = nodeConn.Close()
		waitFinished()
		select {
		case <-reconciled:
		case <-time.After(3 * time.Second):
			t.Error("node-connected reconciliation did not finish")
		}
	}()
	assertSecurityHeaders(t, nodeResponse.Header)
	if err := nodeConn.WriteMessage(websocket.TextMessage, []byte(`{"memory_usage":15}`)); err != nil {
		t.Fatal(err)
	}
	_ = nodeConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, reply, err := nodeConn.ReadMessage()
		if err != nil {
			t.Fatalf("node heartbeat reply: %v", err)
		}
		plain := crypto.DecryptIfNeeded(reply, node.Secret)
		if string(plain) == `{"type":"call"}` {
			break
		}
		// 真实上线对账可并发发送命令；不假定它与心跳确认的网络帧顺序。
		var command struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(plain, &command) != nil || command.RequestID == "" {
			t.Fatalf("unexpected node message before heartbeat reply: %q", plain)
		}
	}
	_ = admin.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, raw, err := admin.ReadMessage()
		if err != nil {
			t.Fatalf("admin heartbeat broadcast: %v", err)
		}
		var message struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		// 上线通知 data 为数字，跳过后等待实际系统信息广播。
		if json.Unmarshal(raw, &message) == nil && message.Type == "info" {
			if message.Data != `{"memory_usage":15}` {
				t.Fatalf("heartbeat broadcast=%q", message.Data)
			}
			break
		}
	}

	for _, tc := range []struct {
		name, query, protocol, origin string
		status                        int
	}{
		{"invalid node secret", "?type=1&secret=wrong", "", "", http.StatusUnauthorized},
		{"rejected node origin", "?type=1&secret=" + url.QueryEscape(node.Secret), "", "https://other.example.com", http.StatusForbidden},
		{"regular user", "?type=0", "jwt." + userToken, "https://panel.example.com", http.StatusUnauthorized},
		{"invalid subprotocol overrides query", "?type=0&secret=" + url.QueryEscape(token), "jwt.invalid", "https://panel.example.com", http.StatusUnauthorized},
		{"rejected admin origin", "?type=0", "jwt." + token, "https://other.example.com", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialer := websocket.Dialer{}
			if tc.protocol != "" {
				dialer.Subprotocols = []string{tc.protocol}
			}
			header := http.Header{}
			if tc.origin != "" {
				header.Set("Origin", tc.origin)
			}
			conn, response, err := dialer.Dial(wsURL+tc.query, header)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatalf("handshake=%v response=%v, want %d", err, response, tc.status)
			}
			defer response.Body.Close()
			waitFinished()
			assertSecurityHeaders(t, response.Header)
		})
	}
}
