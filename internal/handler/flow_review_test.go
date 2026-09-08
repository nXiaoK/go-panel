package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
)

func TestFlowAcknowledgementBodyCompletesBeforeHandlerReturns(t *testing.T) {
	release := make(chan struct{})
	router := gin.New()
	router.GET("/ack", func(c *gin.Context) {
		writeFlowAcknowledgement(c, "application/json", []byte(`{"committed":true}`))
		<-release
	})
	server := httptest.NewServer(router)
	defer server.Close()
	defer close(release)
	client := server.Client()
	client.Timeout = time.Second
	response, err := client.Get(server.URL + "/ack")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != `{"committed":true}` {
		t.Fatalf("ACK body waited for cleanup: %q, %v", body, err)
	}
}

func TestFlowAcknowledgementPrecedesQuotaRuntimeCleanup(t *testing.T) {
	for _, route := range []string{"gost", "nft", "nft-v2"} {
		t.Run(route, func(t *testing.T) {
			mode := "nftables"
			if route == "gost" {
				mode = "gost"
			}
			fx := setupFlowHandlerFixture(t, mode)
			if err := model.DB.Model(&fx.user).Update("flow", 0).Error; err != nil {
				t.Fatal(err)
			}
			var writer *httptest.ResponseRecorder
			checked := false
			callback := "test:ack-before-quota-cleanup"
			if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]interface{})
				if !ok || tx.Statement.Table != "forward" || values["status"] != 0 {
					return
				}
				checked = true
				if writer == nil || !writer.Flushed || writer.Code != http.StatusOK || writer.Header().Get("Content-Length") != strconv.Itoa(writer.Body.Len()) {
					t.Error("quota cleanup ran before a complete ACK was flushed")
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { model.DB.Callback().Update().Remove(callback) })
			r := flowHandlerRouter()
			r.POST("/flow/nft-upload-v2", uploadNftFlowBatchV2)
			item := map[string]any{"forwardId": fx.forward.ID, "userId": fx.user.ID, "userTunnelId": fx.userTunnel.ID, "up": 10, "down": 20}
			var body any = map[string]any{"items": []any{item}}
			path := "/flow/nft-upload"
			if route == "gost" {
				path = "/flow/upload"
				body = map[string]any{"n": fmt.Sprintf("%d_%d_%d", fx.forward.ID, fx.user.ID, fx.userTunnel.ID), "u": 10, "d": 20}
			} else if route == "nft-v2" {
				path = "/flow/nft-upload-v2"
				body = map[string]any{"reporterId": "ack-test", "sequence": 1, "batchId": "batch-1", "items": []any{item}}
			}
			writer = httptest.NewRecorder()
			// 固定 recorder 后再发送，回调才能检查清理发生当时的响应状态。
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
			request.Header.Set("X-Node-Secret", fx.nodeA.Secret)
			r.ServeHTTP(writer, request)
			if !checked {
				t.Fatalf("quota cleanup was skipped: status=%d body=%s", writer.Code, writer.Body.String())
			}
			assertHandlerFlowCounters(t, fx, 20, 10)
		})
	}
}
