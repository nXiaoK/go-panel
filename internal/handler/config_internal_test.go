package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

func configTestRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	crypto.InitJwt("config-test-secret-01234567890123456789")
	expires := int64(1<<62 - 1)
	admin := model.User{ExpTime: &expires, User: "config-admin", Pwd: "unused", RoleID: 0, Status: model.UserStatusActive}
	if err := model.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	token, err := crypto.GenerateToken(admin.ID, admin.User, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)
	return router, token
}

func configRequest(t *testing.T, router http.Handler, token, path, body string) result.R {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", out.Code, out.Body.String())
	}
	var res result.R
	if err := json.Unmarshal(out.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGenericConfigRejectsInternalKeysAndDedicatedAPIStillWorks(t *testing.T) {
	router, token := configTestRouter(t)
	keys := []string{"subscription_api_key", "subscription_default_backfilled", "subscription_v2rayn_default_backfilled", "subscription_singbox_default_backfilled", "subscription_snell_clash_udp_backfilled", "subscription_future_backfilled", "backup_r2_secret_access_key"}
	for _, key := range keys {
		if err := model.DB.Create(&model.ViteConfig{Name: key, Value: "before"}).Error; err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(map[string]string{key: "after"})
		res := configRequest(t, router, token, "/config/update", string(encoded))
		if res.Code == 0 || res.Msg != "该配置项由系统内部维护，不能通过通用配置修改" {
			t.Fatalf("%s accepted: %+v", key, res)
		}
		if res := service.UpdateConfig(key, "single-after"); res.Code == 0 {
			t.Fatalf("single accepted %s", key)
		}
		if got := service.GetConfigValue(key); got != "before" {
			t.Fatalf("%s changed to %q", key, got)
		}
	}
	res := configRequest(t, router, token, "/config/list", "{}")
	if res.Code != 0 {
		t.Fatalf("list: %+v", res)
	}
	configs := res.Data.(map[string]interface{})
	for _, key := range keys {
		if _, ok := configs[key]; ok {
			t.Fatalf("list leaked %s", key)
		}
	}
	res = configRequest(t, router, token, "/sub/api-key", `{"apiKey":"dedicated-api-key-123456789"}`)
	if res.Code != 0 || service.GetConfigValue("subscription_api_key") != "dedicated-api-key-123456789" {
		t.Fatalf("dedicated API failed: %+v", res)
	}
}

func TestDeleteConfigAllowsOnlyAdminsAndCustomEntries(t *testing.T) {
	router, token := configTestRouter(t)
	if res := service.UpdateConfig("custom_test", "value"); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	for attempt := 0; attempt < 2; attempt++ {
		res := configRequest(t, router, token, "/config/delete", `{"name":"custom_test"}`)
		if res.Code != 0 {
			t.Fatalf("delete attempt %d: %+v", attempt, res)
		}
	}
	if res := service.GetConfigByName("custom_test"); res.Code == 0 {
		t.Fatal("custom config still exists")
	}
	for _, name := range []string{"ip", "app_name", "github_download_proxy", "allow_insecure_node_downloads", "allow_insecure_node_downloads_env_override", "subscription_api_key", "subscription_default_backfilled", "backup_r2_enabled"} {
		body, _ := json.Marshal(map[string]string{"name": name})
		if res := configRequest(t, router, token, "/config/delete", string(body)); res.Code == 0 {
			t.Fatalf("deleted protected %s", name)
		}
	}
	expires := int64(1<<62 - 1)
	user := model.User{User: "config-user", Pwd: "unused", RoleID: 1, Status: model.UserStatusActive, ExpTime: &expires}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	userToken, err := crypto.GenerateToken(user.ID, user.User, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res := service.UpdateConfig("custom_kept", "value"); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	for _, unauthorizedToken := range []string{"", userToken} {
		if res := configRequest(t, router, unauthorizedToken, "/config/delete", `{"name":"custom_kept"}`); res.Code != 401 && res.Code != 403 {
			t.Fatalf("unauthorized result: %+v", res)
		}
	}
	if service.GetConfigValue("custom_kept") != "value" {
		t.Fatal("unauthorized request deleted config")
	}
}
