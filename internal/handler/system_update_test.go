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

func TestUpdateStatusRequiresAdmin(t *testing.T) {
	service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: false})
	t.Cleanup(func() { service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: true}) })
	testSystemAdminRoute(t, "/api/v1/system/update/status")
}

func TestUpdateActionsRequireAdminAndStrictJSON(t *testing.T) {
	// 关闭外部版本查询，避免测试依赖 GitHub；权限链与参数解析照常执行。
	service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: false})
	t.Cleanup(func() { service.ConfigureUpdateRuntime(service.UpdateRuntimeConfig{Enabled: true}) })
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)

	token := func(name string, roleID int) string {
		expires := time.Now().Add(time.Hour).UnixMilli()
		user := model.User{ExpTime: &expires, User: name, Pwd: "unused", RoleID: roleID, Status: model.UserStatusActive}
		if err := model.DB.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		value, err := crypto.GenerateToken(user.ID, user.User, roleID, 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	admin, regular := token("update-admin", 0), token("update-user", 1)
	call := func(route, auth, body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", auth)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		var response struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatalf("%s: %v (%s)", route, err, out.Body.String())
		}
		return response.Code, response.Msg
	}

	for _, route := range []string{"/api/v1/system/update/apply", "/api/v1/system/update/cancel", "/api/v1/system/update/rollback"} {
		if code, _ := call(route, regular, "{}"); code != 403 {
			t.Fatalf("%s: regular user code=%d, want 403", route, code)
		}
		if _, msg := call(route, admin, `{"unexpected":true}`); msg != "参数错误" {
			t.Fatalf("%s: unknown field msg=%q", route, msg)
		}
		if _, msg := call(route, admin, `{"jobId":"a","jobId":"b"}`); msg != "参数错误" {
			t.Fatalf("%s: duplicate keys msg=%q", route, msg)
		}
	}
	if code, msg := call("/api/v1/system/update/apply", admin, ""); code == 0 || !strings.Contains(msg, "最新稳定版") {
		t.Fatalf("apply with empty body code=%d msg=%q", code, msg)
	}
	if code, msg := call("/api/v1/system/update/cancel", admin, `{"jobId":"missing"}`); code == 0 || !strings.Contains(msg, "没有可取消") {
		t.Fatalf("cancel code=%d msg=%q", code, msg)
	}
	if code, msg := call("/api/v1/system/update/rollback", admin, `{"target":"sideways"}`); code == 0 || !strings.Contains(msg, "previous 或 builtin") {
		t.Fatalf("rollback code=%d msg=%q", code, msg)
	}
}
