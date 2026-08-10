package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestConfigReadsFilterSensitiveValuesForNonAdmins(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	updateOrCreateConfig("app_name", "Flux Panel")
	updateOrCreateConfig("subscription_api_key", "secret-key")
	updateOrCreateConfig("custom_internal", "private")

	// id=1 为迁移创建的默认管理员（role_id=0）。再建一个普通用户用于对比。
	exp := int64(1)<<62 - 1
	normalUser := model.User{User: "normal", RoleID: userRoleID, Status: userStatusActive, ExpTime: &exp}
	if err := model.DB.Create(&normalUser).Error; err != nil {
		t.Fatalf("create normal user: %v", err)
	}

	adminConfigs := GetConfigsForUser(1).Data.(map[string]string)
	if adminConfigs["subscription_api_key"] != "secret-key" {
		t.Fatalf("admin config should include subscription_api_key, got %#v", adminConfigs)
	}

	userConfigs := GetConfigsForUser(normalUser.ID).Data.(map[string]string)
	if userConfigs["app_name"] != "Flux Panel" {
		t.Fatalf("user config should include app_name, got %#v", userConfigs)
	}
	if _, ok := userConfigs["subscription_api_key"]; ok {
		t.Fatalf("user config leaked subscription_api_key: %#v", userConfigs)
	}
	if _, ok := userConfigs["custom_internal"]; ok {
		t.Fatalf("user config leaked custom_internal: %#v", userConfigs)
	}

	if res := GetPublicConfigByName("subscription_api_key"); res.Code != 403 {
		t.Fatalf("public secret read code=%d, want 403", res.Code)
	}
	if res := GetPublicConfigByName("app_name"); res.Code != 0 {
		t.Fatalf("public app_name read code=%d msg=%s", res.Code, res.Msg)
	}
	if res := GetPublicConfigByName(" app_name "); res.Code != 0 {
		t.Fatalf("trimmed public app_name read code=%d msg=%s", res.Code, res.Msg)
	}

	// 管理员被降级后不应再读到敏感配置（旧 token 角色失效场景）。
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("role_id", userRoleID).Error; err != nil {
		t.Fatalf("demote admin: %v", err)
	}
	demotedConfigs := GetConfigsForUser(1).Data.(map[string]string)
	if _, ok := demotedConfigs["subscription_api_key"]; ok {
		t.Fatalf("demoted user config leaked subscription_api_key: %#v", demotedConfigs)
	}
}

func TestAllowInsecureNodeDownloadsConfigIsStrictAndNormalized(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })

	invalid := UpdateConfigs(map[string]string{
		"app_name":                           "不应部分保存",
		allowInsecureNodeDownloadsConfigName: "yes",
	})
	if invalid.Code == 0 {
		t.Fatal("invalid HTTP node download setting was accepted")
	}
	if got := GetConfigValue("app_name"); got == "不应部分保存" {
		t.Fatal("batch config validation partially saved another setting")
	}

	if res := UpdateConfig(allowInsecureNodeDownloadsConfigName, " TRUE "); res.Code != 0 {
		t.Fatalf("enable HTTP node downloads: code=%d msg=%s", res.Code, res.Msg)
	}
	if got := GetConfigValue(allowInsecureNodeDownloadsConfigName); got != "true" {
		t.Fatalf("normalized setting=%q, want true", got)
	}
	if res := UpdateConfigs(map[string]string{allowInsecureNodeDownloadsConfigName: "false"}); res.Code != 0 {
		t.Fatalf("disable HTTP node downloads: code=%d msg=%s", res.Code, res.Msg)
	}
	if got := GetConfigValue(allowInsecureNodeDownloadsConfigName); got != "false" {
		t.Fatalf("disabled setting=%q, want false", got)
	}
}

func TestConfigWriteFailuresAreReportedAndBatchRollsBack(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })

	if err := updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "true"); err != nil {
		t.Fatalf("seed HTTP switch: %v", err)
	}
	if err := updateOrCreateConfig("a_config", "before"); err != nil {
		t.Fatalf("seed other config: %v", err)
	}
	// 模拟 SQLite 在关闭危险开关时写失败；服务不能谎报成功，也不能保留同批更早写入的其他键。
	if err := model.DB.Exec(`CREATE TRIGGER fail_disable_insecure_node_downloads
		BEFORE UPDATE OF value ON vite_config
		WHEN OLD.name = 'allow_insecure_node_downloads' AND NEW.value = 'false'
		BEGIN
		  SELECT RAISE(FAIL, 'injected config write failure');
		END`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if err := updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "false"); err == nil {
		t.Fatal("updateOrCreateConfig swallowed the injected write failure")
	}

	single := UpdateConfig(allowInsecureNodeDownloadsConfigName, "false")
	if single.Code == 0 || !strings.Contains(single.Msg, "配置更新失败") {
		t.Fatalf("single write failure response=%+v", single)
	}
	if got := GetConfigValue(allowInsecureNodeDownloadsConfigName); got != "true" {
		t.Fatalf("failed single update changed HTTP switch to %q", got)
	}

	batch := UpdateConfigs(map[string]string{
		"a_config":                           "after",
		"a_new_config":                       "must-roll-back",
		allowInsecureNodeDownloadsConfigName: "false",
	})
	if batch.Code == 0 || !strings.Contains(batch.Msg, "配置更新失败") {
		t.Fatalf("batch write failure response=%+v", batch)
	}
	if got := GetConfigValue(allowInsecureNodeDownloadsConfigName); got != "true" {
		t.Fatalf("failed batch changed HTTP switch to %q", got)
	}
	if got := GetConfigValue("a_config"); got != "before" {
		t.Fatalf("failed batch partially saved existing key=%q", got)
	}
	if got := GetConfigValue("a_new_config"); got != "" {
		t.Fatalf("failed batch partially created new key=%q", got)
	}
}

func TestAdminConfigListIncludesReadOnlyHTTPEnvironmentOverride(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		ConfigureNodeRuntime(NodeRuntimeConfig{})
		_ = model.Close()
	})
	ConfigureNodeRuntime(NodeRuntimeConfig{AllowInsecureDownloads: true})
	if err := updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "false"); err != nil {
		t.Fatalf("seed database HTTP switch: %v", err)
	}
	if err := updateOrCreateConfig("app_name", "before"); err != nil {
		t.Fatalf("seed app name: %v", err)
	}

	adminConfigs := GetConfigsForUser(1).Data.(map[string]string)
	if adminConfigs[allowInsecureNodeDownloadsConfigName] != "false" {
		t.Fatalf("admin database switch=%q, want false", adminConfigs[allowInsecureNodeDownloadsConfigName])
	}
	if adminConfigs[allowInsecureNodeDownloadsEnvOverrideName] != "true" {
		t.Fatalf("admin environment override=%q, want true", adminConfigs[allowInsecureNodeDownloadsEnvOverrideName])
	}

	exp := int64(1)<<62 - 1
	normalUser := model.User{User: "normal-env", RoleID: userRoleID, Status: userStatusActive, ExpTime: &exp}
	if err := model.DB.Create(&normalUser).Error; err != nil {
		t.Fatalf("create normal user: %v", err)
	}
	userConfigs := GetConfigsForUser(normalUser.ID).Data.(map[string]string)
	if _, ok := userConfigs[allowInsecureNodeDownloadsEnvOverrideName]; ok {
		t.Fatalf("normal user saw runtime environment override: %#v", userConfigs)
	}

	if res := UpdateConfig(allowInsecureNodeDownloadsEnvOverrideName, "false"); res.Code == 0 {
		t.Fatalf("single update accepted read-only derived field: %+v", res)
	}
	if res := UpdateConfigs(map[string]string{
		"app_name": "must-not-save",
		allowInsecureNodeDownloadsEnvOverrideName: "false",
	}); res.Code == 0 {
		t.Fatalf("batch update accepted read-only derived field: %+v", res)
	}
	if got := GetConfigValue("app_name"); got != "before" {
		t.Fatalf("read-only field rejection partially saved app_name=%q", got)
	}
	if got := GetConfigValue(allowInsecureNodeDownloadsEnvOverrideName); got != "" {
		t.Fatalf("derived environment override was persisted as database config=%q", got)
	}

	ConfigureNodeRuntime(NodeRuntimeConfig{})
	adminConfigs = GetConfigsForUser(1).Data.(map[string]string)
	if adminConfigs[allowInsecureNodeDownloadsEnvOverrideName] != "false" {
		t.Fatalf("admin environment override=%q after reset, want false", adminConfigs[allowInsecureNodeDownloadsEnvOverrideName])
	}
}
