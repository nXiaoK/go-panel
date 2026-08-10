package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
)

type flowHandlerFixture struct {
	nodeA      model.Node
	nodeB      model.Node
	user       model.User
	tunnel     model.Tunnel
	forward    model.Forward
	userTunnel model.UserTunnel
}

func setupFlowHandlerFixture(t *testing.T, mode string) flowHandlerFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	nodeA := model.Node{
		Name: "handler-entry-a", Secret: "handler-entry-a-secret", IP: "192.0.2.11", ServerIP: "192.0.2.11",
		PortSta: 10000, PortEnd: 20000, ForwardMode: mode, CreatedTime: now, Status: 1,
	}
	nodeB := model.Node{
		Name: "handler-entry-b", Secret: "handler-entry-b-secret", IP: "192.0.2.12", ServerIP: "192.0.2.12",
		PortSta: 20001, PortEnd: 30000, ForwardMode: mode, CreatedTime: now, Status: 1,
	}
	for _, node := range []*model.Node{&nodeA, &nodeB} {
		if err := model.DB.Create(node).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
		service.InvalidateSecretCache(node.Secret)
		t.Cleanup(func() { service.InvalidateSecretCache(node.Secret) })
	}
	expires := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{
		User: "handler-flow-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: now, Num: 1, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tunnel := model.Tunnel{
		Name: "handler-flow-tunnel", TrafficRatio: 1, InNodeID: nodeA.ID, InIP: nodeA.IP,
		OutNodeID: nodeA.ID, OutIP: nodeA.IP, Type: 1, Flow: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	forward := model.Forward{
		UserID: user.ID, UserName: user.User, Name: "handler-flow-forward", TunnelID: tunnel.ID,
		InPort: 10001, RemoteAddr: "198.51.100.11:443", Strategy: "fifo",
		CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	userTunnel := model.UserTunnel{
		UserID: user.ID, TunnelID: tunnel.ID, Num: 1, Flow: 100,
		FlowResetTime: now, ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&userTunnel).Error; err != nil {
		t.Fatalf("create user tunnel: %v", err)
	}
	return flowHandlerFixture{nodeA: nodeA, nodeB: nodeB, user: user, tunnel: tunnel, forward: forward, userTunnel: userTunnel}
}

func flowHandlerRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/flow/upload", uploadFlowData)
	r.POST("/flow/nft-upload", uploadNftFlowBatch)
	return r
}

func performFlowRequest(t *testing.T, r http.Handler, path, secret string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Node-Secret", secret)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUploadFlowRejectsCredentialsAndMalformedPayloadWithSafeJSON(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "gost")
	r := flowHandlerRouter()

	w := performFlowRequest(t, r, "/flow/upload", "private-invalid-secret", map[string]any{"n": "web_api"})
	assertSafeFlowError(t, w, "private-invalid-secret")

	req := httptest.NewRequest(http.MethodPost, "/flow/upload", strings.NewReader("{"))
	req.Header.Set("X-Node-Secret", fixture.nodeA.Secret)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertSafeFlowError(t, w, fixture.nodeA.Secret)
}

func TestUploadGostFlowCommitsOnlyForAuthenticatedEntryNode(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "gost")
	r := flowHandlerRouter()
	body := map[string]any{
		"n": fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		"u": 100,
		"d": 200,
	}

	rejected := performFlowRequest(t, r, "/flow/upload", fixture.nodeB.Secret, body)
	assertSafeFlowError(t, rejected, fixture.nodeB.Secret)
	assertHandlerFlowCounters(t, fixture, 0, 0)

	accepted := performFlowRequest(t, r, "/flow/upload", fixture.nodeA.Secret, body)
	if accepted.Code != http.StatusOK || accepted.Body.String() != successResponse {
		t.Fatalf("status=%d body=%q, want committed ok", accepted.Code, accepted.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 200, 100)
}

func TestUploadGostFlowEnforcesCommittedUserLimits(t *testing.T) {
	tests := []struct {
		name   string
		update map[string]any
	}{
		{name: "flow quota", update: map[string]any{"flow": int64(0)}},
		{name: "expired", update: map[string]any{"exp_time": time.Now().Add(-time.Minute).UnixMilli()}},
		{name: "disabled", update: map[string]any{"status": 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupFlowHandlerFixture(t, "gost")
			if err := model.DB.Model(&model.User{}).Where("id = ?", fixture.user.ID).Updates(tc.update).Error; err != nil {
				t.Fatalf("set user limit state: %v", err)
			}
			r := flowHandlerRouter()
			body := map[string]any{
				"n": fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
				"u": 100, "d": 200,
			}
			w := performFlowRequest(t, r, "/flow/upload", fixture.nodeA.Secret, body)
			if w.Code != http.StatusOK || w.Body.String() != successResponse {
				t.Fatalf("status=%d body=%q, want committed ok", w.Code, w.Body.String())
			}
			assertHandlerFlowCounters(t, fixture, 200, 100)
			assertHandlerForwardStatus(t, fixture.forward.ID, 0)
		})
	}
}

func TestUploadNftFlowBatchRollsBackWhenAnyItemIsRejected(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", fixture.userTunnel.ID).Update("flow", 0).Error; err != nil {
		t.Fatalf("set exhausted user tunnel quota: %v", err)
	}
	r := flowHandlerRouter()
	valid := map[string]any{
		"forwardId": fixture.forward.ID, "userId": fixture.user.ID, "userTunnelId": fixture.userTunnel.ID,
		"up": 100, "down": 200,
	}
	forged := map[string]any{
		"forwardId": fixture.forward.ID, "userId": fixture.user.ID + 999, "userTunnelId": fixture.userTunnel.ID,
		"up": 1, "down": 2,
	}

	rejected := performFlowRequest(t, r, "/flow/nft-upload", fixture.nodeA.Secret, map[string]any{"items": []any{valid, forged}})
	assertSafeFlowError(t, rejected, fixture.nodeA.Secret)
	assertHandlerFlowCounters(t, fixture, 0, 0)
	assertHandlerForwardStatus(t, fixture.forward.ID, 1)

	accepted := performFlowRequest(t, r, "/flow/nft-upload", fixture.nodeA.Secret, map[string]any{"items": []any{valid}})
	if accepted.Code != http.StatusOK || accepted.Body.String() != successResponse {
		t.Fatalf("status=%d body=%q, want committed ok", accepted.Code, accepted.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 200, 100)
	assertHandlerForwardStatus(t, fixture.forward.ID, 0)
}

func TestUploadNftFlowBatchEnforcesCommittedUserTunnelLimits(t *testing.T) {
	tests := []struct {
		name   string
		update map[string]any
	}{
		{name: "flow quota", update: map[string]any{"flow": int64(0)}},
		{name: "expired", update: map[string]any{"exp_time": time.Now().Add(-time.Minute).UnixMilli()}},
		{name: "disabled", update: map[string]any{"status": 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupFlowHandlerFixture(t, "nftables")
			if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", fixture.userTunnel.ID).Updates(tc.update).Error; err != nil {
				t.Fatalf("set user tunnel limit state: %v", err)
			}
			r := flowHandlerRouter()
			item := map[string]any{
				"forwardId": fixture.forward.ID, "userId": fixture.user.ID, "userTunnelId": fixture.userTunnel.ID,
				"up": 100, "down": 200,
			}
			w := performFlowRequest(t, r, "/flow/nft-upload", fixture.nodeA.Secret, map[string]any{"items": []any{item, item}})
			if w.Code != http.StatusOK || w.Body.String() != successResponse {
				t.Fatalf("status=%d body=%q, want committed ok", w.Code, w.Body.String())
			}
			assertHandlerFlowCounters(t, fixture, 400, 200)
			assertHandlerForwardStatus(t, fixture.forward.ID, 0)
		})
	}
}

func TestNftBatchV2ReturnsMatchingAckAndReplaysWithoutDoubleCount(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	r := gin.New()
	Register(r, "", false)
	body := map[string]any{
		"reporterId": "handler-reporter-1", "sequence": 1, "batchId": "handler-batch-1",
		"capturedAt": time.Now().Add(-time.Hour).UnixMilli(),
		"items": []any{map[string]any{
			"forwardId": fixture.forward.ID, "userId": fixture.user.ID, "userTunnelId": fixture.userTunnel.ID,
			"up": 100, "down": 200,
		}},
	}

	first := performFlowRequest(t, r, "/flow/nft-upload-v2", fixture.nodeA.Secret, body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var ack dto.NftFlowAckDto
	if err := json.Unmarshal(first.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.ReporterID != "handler-reporter-1" || ack.Sequence != 1 || ack.BatchID != "handler-batch-1" || len(ack.AckDigest) != 64 {
		t.Fatalf("ack=%+v", ack)
	}
	retry := performFlowRequest(t, r, "/flow/nft-upload-v2", fixture.nodeA.Secret, body)
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Fatalf("retry status=%d body=%s, want exact ack %s", retry.Code, retry.Body.String(), first.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 200, 100)
}

func TestUploadNftV2RejectsUnknownDuplicateAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		body func(flowHandlerFixture) string
	}{
		{
			name: "duplicate reporter id",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","reporterId":"reporter-b","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2}]}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
		{
			name: "duplicate nested up",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"up":3,"down":2}]}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
		{
			name: "duplicate nested down",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2,"down":3}]}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
		{
			name: "unknown top-level field",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2}],"unknown":true}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
		{
			name: "unknown nested field",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2,"unknown":true}]}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
		{
			name: "trailing document",
			body: func(f flowHandlerFixture) string {
				return fmt.Sprintf(`{"reporterId":"reporter-a","sequence":1,"batchId":"batch-a","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2}]} {}`,
					f.forward.ID, f.user.ID, f.userTunnel.ID)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupFlowHandlerFixture(t, "nftables")
			r := gin.New()
			Register(r, "", false)
			w := performRawFlowRequest(r, "/flow/nft-upload-v2", fixture.nodeA.Secret, tc.body(fixture))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
			}
			assertSafeFlowError(t, w, fixture.nodeA.Secret)
			assertHandlerFlowCounters(t, fixture, 0, 0)
		})
	}
}

func TestUploadNftV2AcceptsStrictValidJSONObject(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	r := gin.New()
	Register(r, "", false)
	body := fmt.Sprintf(`{"reporterId":"strict-reporter","sequence":1,"batchId":"strict-batch","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2}]}`,
		fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID)
	w := performRawFlowRequest(r, "/flow/nft-upload-v2", fixture.nodeA.Secret, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 2, 1)
}

func TestUploadNftV2StrictDecodePreservesEncryptedRequests(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	r := gin.New()
	Register(r, "", false)
	body := fmt.Sprintf(`{"reporterId":"encrypted-reporter","sequence":1,"batchId":"encrypted-batch","items":[{"forwardId":%d,"userId":%d,"userTunnelId":%d,"up":1,"down":2}]}`,
		fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID)
	encrypted := crypto.EncryptIfPossible([]byte(body), fixture.nodeA.Secret, time.Now().UnixMilli())
	w := performRawFlowRequest(r, "/flow/nft-upload-v2", fixture.nodeA.Secret, string(encrypted))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 2, 1)
}

func TestUploadNftV2KeepsRequestBodyLimit(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	r := gin.New()
	Register(r, "", false)
	w := performRawFlowRequest(r, "/flow/nft-upload-v2", fixture.nodeA.Secret, strings.Repeat("x", int(maxFlowUploadBodySize)+1))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%q, want 413", w.Code, w.Body.String())
	}
	assertSafeFlowError(t, w, fixture.nodeA.Secret)
	assertHandlerFlowCounters(t, fixture, 0, 0)
}

func performRawFlowRequest(r http.Handler, path, secret, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestNftBatchLegacyUploadRequiresExplicitEscapeHatch(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "nftables")
	item := map[string]any{
		"forwardId": fixture.forward.ID, "userId": fixture.user.ID, "userTunnelId": fixture.userTunnel.ID,
		"up": 100, "down": 200,
	}
	body := map[string]any{"items": []any{item}}

	disabled := gin.New()
	Register(disabled, "", false)
	w := performFlowRequest(t, disabled, "/flow/nft-upload", fixture.nodeA.Secret, body)
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("disabled status=%d body=%s, want 426", w.Code, w.Body.String())
	}
	assertSafeFlowError(t, w, fixture.nodeA.Secret)
	assertHandlerFlowCounters(t, fixture, 0, 0)

	enabled := gin.New()
	Register(enabled, "", true)
	w = performFlowRequest(t, enabled, "/flow/nft-upload", fixture.nodeA.Secret, body)
	if w.Code != http.StatusOK || w.Body.String() != successResponse {
		t.Fatalf("enabled status=%d body=%q", w.Code, w.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 200, 100)
}

func TestUploadGostFlowReturnsFailureAndRollsBackOnDatabaseError(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "gost")
	r := flowHandlerRouter()
	if err := model.DB.Model(&model.User{}).Where("id = ?", fixture.user.ID).Update("flow", 0).Error; err != nil {
		t.Fatalf("set exhausted user quota: %v", err)
	}
	trigger := fmt.Sprintf(`
CREATE TRIGGER fail_handler_flow_user_update
BEFORE UPDATE OF in_flow, out_flow ON user
WHEN OLD.id = %d
BEGIN
  SELECT RAISE(FAIL, 'injected handler flow update failure');
END`, fixture.user.ID)
	if err := model.DB.Exec(trigger).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	body := map[string]any{
		"n": fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		"u": 100,
		"d": 200,
	}

	w := performFlowRequest(t, r, "/flow/upload", fixture.nodeA.Secret, body)
	assertSafeFlowError(t, w, fixture.nodeA.Secret)
	assertHandlerFlowCounters(t, fixture, 0, 0)
	assertHandlerForwardStatus(t, fixture.forward.ID, 1)
}

func TestUploadFlowWebAPIAcknowledgesOnlyAuthenticatedNodeAndDoesNotWrite(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "gost")
	r := flowHandlerRouter()
	w := performFlowRequest(t, r, "/flow/upload", fixture.nodeA.Secret, map[string]any{"n": "web_api", "u": 999, "d": 999})
	if w.Code != http.StatusOK || w.Body.String() != successResponse {
		t.Fatalf("status=%d body=%q, want authenticated no-op acknowledgement", w.Code, w.Body.String())
	}
	assertHandlerFlowCounters(t, fixture, 0, 0)
}

func TestUploadFlowKeepsRequestBodyLimit(t *testing.T) {
	fixture := setupFlowHandlerFixture(t, "gost")
	r := flowHandlerRouter()
	req := httptest.NewRequest(http.MethodPost, "/flow/upload", strings.NewReader(strings.Repeat("x", int(maxFlowUploadBodySize)+1)))
	req.Header.Set("X-Node-Secret", fixture.nodeA.Secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%q, want 413", w.Code, w.Body.String())
	}
	assertSafeFlowError(t, w, fixture.nodeA.Secret)
}

func assertSafeFlowError(t *testing.T, w *httptest.ResponseRecorder, forbidden string) {
	t.Helper()
	if w.Code >= 200 && w.Code < 300 {
		t.Fatalf("status=%d body=%q, want non-2xx", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error body is not JSON: %q: %v", w.Body.String(), err)
	}
	if _, ok := payload["error"]; !ok {
		t.Fatalf("error JSON=%v, want error field", payload)
	}
	if forbidden != "" && strings.Contains(w.Body.String(), forbidden) {
		t.Fatalf("error body leaked secret %q: %s", forbidden, w.Body.String())
	}
}

func assertHandlerFlowCounters(t *testing.T, fixture flowHandlerFixture, in, out int64) {
	t.Helper()
	var forward model.Forward
	var user model.User
	var userTunnel model.UserTunnel
	if err := model.DB.First(&forward, fixture.forward.ID).Error; err != nil {
		t.Fatalf("load forward: %v", err)
	}
	if err := model.DB.First(&user, fixture.user.ID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if err := model.DB.First(&userTunnel, fixture.userTunnel.ID).Error; err != nil {
		t.Fatalf("load user tunnel: %v", err)
	}
	for label, counters := range map[string][2]int64{
		"forward": {forward.InFlow, forward.OutFlow}, "user": {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{in, out} {
			t.Fatalf("%s counters=%v, want [%d %d]", label, counters, in, out)
		}
	}
}

func assertHandlerForwardStatus(t *testing.T, forwardID int64, want int) {
	t.Helper()
	var forward model.Forward
	if err := model.DB.First(&forward, forwardID).Error; err != nil {
		t.Fatalf("load forward status: %v", err)
	}
	if forward.Status != want {
		t.Fatalf("forward status=%d, want %d", forward.Status, want)
	}
}
