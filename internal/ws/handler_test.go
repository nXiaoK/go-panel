package ws

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestNormalizeReportedPanelBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "https base path", raw: "https://panel.example.com/base/", want: "https://panel.example.com/base"},
		{name: "proxy external address", raw: "https://external.example.com:8443/proxy/", want: "https://external.example.com:8443/proxy"},
		{name: "ipv6", raw: "http://[2001:db8::1]:6365/", want: "http://[2001:db8::1]:6365"},
		{name: "userinfo", raw: "https://user:pass@panel.example.com", wantErr: true},
		{name: "query", raw: "https://panel.example.com?secret=bad", wantErr: true},
		{name: "empty query marker", raw: "https://panel.example.com?", wantErr: true},
		{name: "websocket scheme", raw: "wss://panel.example.com", wantErr: true},
		{name: "encoded traversal", raw: "https://panel.example.com/%2e%2e/assets", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeReportedPanelBaseURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("normalized URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNodeWebSocketPersistsReportedPanelBaseURL(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	previousHub := Default
	Default = NewHub()
	defer func() { Default = previousHub }()
	SetAllowedOrigins("")

	node := model.Node{
		Name: "history-node", Secret: "history-node-secret", IP: "127.0.0.1", ServerIP: "127.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: "nftables", CreatedTime: time.Now().UnixMilli(),
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(HandleSystemInfo))
	defer server.Close()
	values := url.Values{}
	values.Set("type", "1")
	values.Set("secret", node.Secret)
	values.Set("version", "nftables-go-1.3.4")
	values.Set("panelUrl", server.URL+"/base/")
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?" + values.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial node websocket: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for {
		var got model.Node
		if err := model.DB.First(&got, node.ID).Error; err != nil {
			t.Fatalf("reload node: %v", err)
		}
		if got.LastConnectedBaseURL != "" {
			if got.LastConnectedBaseURL != server.URL+"/base" || got.LastConnectedBaseTime <= 0 {
				t.Fatalf("stored history = %q at %d", got.LastConnectedBaseURL, got.LastConnectedBaseTime)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reported panel URL was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNodeWebSocketWithoutPanelURLClearsStaleHistory(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	previousHub := Default
	Default = NewHub()
	defer func() { Default = previousHub }()
	SetAllowedOrigins("")

	spoofedVersion := "nftables-go-1.3.4"
	node := model.Node{
		Name: "stale-history-node", Secret: "stale-history-secret", IP: "127.0.0.1", ServerIP: "127.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: "nftables", Version: &spoofedVersion,
		LastConnectedBaseURL: "https://attacker.example.com", LastConnectedBaseTime: 1234,
		CreatedTime: time.Now().UnixMilli(), Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(HandleSystemInfo))
	defer server.Close()
	values := url.Values{}
	values.Set("type", "1")
	values.Set("secret", node.Secret)
	values.Set("version", "nftables-go-1.3.3")
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?" + values.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial legacy node websocket: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for {
		var got model.Node
		if err := model.DB.First(&got, node.ID).Error; err != nil {
			t.Fatalf("reload node: %v", err)
		}
		if got.LastConnectedBaseURL == "" {
			if got.LastConnectedBaseTime != 0 || got.Version == nil || *got.Version != "nftables-go-1.3.3" {
				t.Fatalf("cleared history state = URL %q time %d version %v", got.LastConnectedBaseURL, got.LastConnectedBaseTime, got.Version)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stale history was not cleared: %q", got.LastConnectedBaseURL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebSocketReadLimitClosesOversizeNodeMessageBeforeProcessing(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	Default = NewHub()
	SetAllowedOrigins("")
	node := model.Node{
		Name: "limit-node", Secret: "limit-node-secret", IP: "127.0.0.1", ServerIP: "127.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: "nftables", CreatedTime: time.Now().UnixMilli(),
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(HandleSystemInfo))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?type=1&secret=" + url.QueryEscape(node.Secret)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial node websocket: %v", err)
	}
	defer conn.Close()

	requestID := "oversize-node-message"
	processed := make(chan GostResult, 1)
	Default.pending.Store(requestID, &pendingRequest{nodeID: node.ID, session: Default.GetNode(node.ID), result: processed})
	defer Default.pending.Delete(requestID)
	payload, err := json.Marshal(map[string]string{
		"requestId": requestID,
		"message":   "must-not-process",
		"padding":   strings.Repeat("x", (1<<20)+1),
	})
	if err != nil {
		t.Fatalf("marshal oversized message: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("write oversized node message: %v", err)
	}

	select {
	case got := <-processed:
		if got.Msg != "节点连接已断开" {
			t.Fatalf("oversized node message reached business processing: %#v", got)
		}
	case <-time.After(200 * time.Millisecond):
	}
	assertWebSocketClosedAfterOversize(t, conn)
}

func TestWebSocketReadLimitClosesOversizeAdminMessage(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	Default = NewHub()
	SetAllowedOrigins("")
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 0).Error; err != nil {
		t.Fatalf("unlock admin: %v", err)
	}
	crypto.InitJwt("websocket-limit-test-secret")
	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(HandleSystemInfo))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?type=0&secret=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial admin websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), (1<<20)+1)); err != nil {
		t.Fatalf("write oversized admin message: %v", err)
	}
	assertWebSocketClosedAfterOversize(t, conn)
}

func assertWebSocketClosedAfterOversize(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("oversized websocket message did not close connection")
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("websocket remained open after oversized message: %v", err)
	}
}

func TestBuildIperf3ProgressBroadcast(t *testing.T) {
	raw := json.RawMessage(`{"testId":"live-123","mbps":940.5,"endSeconds":2}`)
	resp := commandResponse{
		RequestId: "req-1",
		Type:      "Iperf3Progress",
		Message:   "progress",
		Data:      raw,
	}

	payload, err := buildIperf3ProgressBroadcast(7, resp)
	if err != nil {
		t.Fatalf("buildIperf3ProgressBroadcast returned error: %v", err)
	}

	var got struct {
		ID   string          `json:"id"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("broadcast JSON invalid: %v", err)
	}
	if got.ID != "7" {
		t.Fatalf("ID=%q, want 7", got.ID)
	}
	if got.Type != "speed-test-progress" {
		t.Fatalf("Type=%q, want speed-test-progress", got.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("data JSON invalid: %v", err)
	}
	if data["testId"] != "live-123" {
		t.Fatalf("testId=%v, want live-123", data["testId"])
	}
}

func TestValidateAdminTokenRequiresActiveAdminUser(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	adminToken, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	if validateAdminToken(adminToken) {
		t.Fatalf("admin requiring password change should not be accepted")
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 0).Error; err != nil {
		t.Fatalf("clear must_change_pwd: %v", err)
	}
	if !validateAdminToken(adminToken) {
		t.Fatalf("active admin token should be accepted")
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 1).Error; err != nil {
		t.Fatalf("set must_change_pwd: %v", err)
	}
	if validateAdminToken(adminToken) {
		t.Fatalf("admin requiring password change should not be accepted")
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 0).Error; err != nil {
		t.Fatalf("clear must_change_pwd again: %v", err)
	}

	exp := time.Now().Add(24 * time.Hour).UnixMilli()
	user := model.User{
		User:          "normal-user",
		Pwd:           "unused",
		RoleID:        1,
		ExpTime:       &exp,
		Flow:          1,
		FlowResetTime: 0,
		Num:           1,
		CreatedTime:   time.Now().UnixMilli(),
		Status:        1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	userToken, err := crypto.GenerateToken(user.ID, user.User, user.RoleID, user.TokenVersion)
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	if validateAdminToken(userToken) {
		t.Fatalf("normal user token should not be accepted as admin")
	}

	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("status", 0).Error; err != nil {
		t.Fatalf("disable admin: %v", err)
	}
	if validateAdminToken(adminToken) {
		t.Fatalf("disabled admin token should not be accepted")
	}
}

func TestValidateAdminTokenRejectsExpiredUser(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	expiredAt := time.Now().Add(-time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"must_change_pwd": 0,
		"exp_time":        expiredAt,
	}).Error; err != nil {
		t.Fatalf("expire admin: %v", err)
	}
	if validateAdminToken(token) {
		t.Fatal("expired admin token should not be accepted")
	}
}

func TestValidateAdminTokenRejectsTokenVersionMismatch(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"must_change_pwd": 0,
		"token_version":   1,
	}).Error; err != nil {
		t.Fatalf("increment admin token version: %v", err)
	}
	if validateAdminToken(token) {
		t.Fatal("revoked admin token should not be accepted")
	}
}
