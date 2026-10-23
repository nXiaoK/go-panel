package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
)

func TestSystemVersionRequiresAdmin(t *testing.T) {
	testSystemAdminRoute(t, "/api/v1/system/version")
}

func TestUpdateCheckRequiresAdmin(t *testing.T) {
	// 测试关闭外部版本查询，真实路由和权限链照常执行，避免依赖 GitHub 网络。
	service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: false})
	t.Cleanup(func() { service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: true}) })
	testSystemAdminRoute(t, "/api/v1/system/update/check")
}

func testSystemAdminRoute(t *testing.T, path string) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)
	for _, roleID := range []int{1, 0} {
		t.Run(map[int]string{0: "administrator", 1: "regular user"}[roleID], func(t *testing.T) {
			expires := time.Now().Add(time.Hour).UnixMilli()
			user := model.User{ExpTime: &expires, User: map[int]string{0: "system-admin", 1: "system-user"}[roleID], Pwd: "unused", RoleID: roleID, Status: model.UserStatusActive}
			if err := model.DB.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			token, err := crypto.GenerateToken(user.ID, user.User, roleID, 0)
			if err != nil {
				t.Fatal(err)
			}
			request := func(route string) map[string]json.RawMessage {
				req := httptest.NewRequest(http.MethodPost, route, strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", token)
				out := httptest.NewRecorder()
				router.ServeHTTP(out, req)
				if out.Code != http.StatusOK {
					t.Fatalf("%s HTTP status=%d: %s", route, out.Code, out.Body.String())
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				return body
			}
			body := request(path)
			var code int
			if err := json.Unmarshal(body["code"], &code); err != nil {
				t.Fatal(err)
			}
			want := 403
			if roleID == 0 {
				want = 0
			}
			if code != want {
				t.Fatalf("%s role=%d: code=%d, want %d; body=%s", path, roleID, code, want, body)
			}
			if roleID == 1 && string(body["data"]) != "null" {
				t.Fatalf("non-admin received data: %s", body)
			}
			status := request("/api/v1/system/status")
			if err := json.Unmarshal(status["code"], &code); err != nil || code != 0 {
				t.Fatalf("system status is not available: %s", status)
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(status["data"], &data); err != nil {
				t.Fatal(err)
			}
			if len(data) != 2 || data["startedAt"] == nil || data["uptimeSeconds"] == nil {
				t.Fatalf("unexpected status fields: %s", data)
			}
		})
	}
}
