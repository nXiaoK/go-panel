package service

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	allowInsecureNodeDownloadsConfigName = "allow_insecure_node_downloads"
	// 该字段只反映进程启动时的环境配置，不写入数据库；为 true 时即使页面开关关闭，仍允许通过 HTTP 下载节点程序。
	allowInsecureNodeDownloadsEnvOverrideName = "allow_insecure_node_downloads_env_override"
	// GitHub 下载代理仅传给订阅服务器脚本；空值保持直连，非空值必须是可信的 HTTPS 全链接代理前缀。
	githubDownloadProxyConfigName = "github_download_proxy"
)

var publicConfigNames = map[string]struct{}{
	"app_name": {},
}

func isPublicConfigName(name string) bool {
	_, ok := publicConfigNames[strings.TrimSpace(name)]
	return ok
}

// GetConfigsForUser 按数据库中的当前用户角色读取配置：管理员返回全部配置，
// 普通用户只返回公开白名单，避免泄露订阅 API Key 等敏感值；同时避免旧 token 角色失效后继续读取敏感配置。
func GetConfigsForUser(userID int64) result.R {
	var user model.User
	if err := model.DB.Select("id, role_id, status").First(&user, userID).Error; err != nil {
		return result.ErrCode(401, "用户不存在")
	}
	return getConfigs(isActiveUserStatus(user.Status) && user.RoleID == adminRoleID)
}

func getConfigs(includeSensitive bool) result.R {
	var list []model.ViteConfig
	model.DB.Find(&list)
	m := make(map[string]string, len(list)+1)
	for _, c := range list {
		// Cloudflare R2 配置含加密凭据和内部运行状态，只能通过专用脱敏接口读取，
		// 即使管理员也不能从通用键值接口取得密文或把内部字段展示为高级配置。
		if isR2BackupConfigName(c.Name) {
			continue
		}
		if !includeSensitive && !isPublicConfigName(c.Name) {
			continue
		}
		m[c.Name] = c.Value
	}
	if includeSensitive {
		// 这是管理员只读运行时状态，不是数据库配置；运行时 true 的优先级高于页面数据库开关。
		m[allowInsecureNodeDownloadsEnvOverrideName] = strconv.FormatBool(nodeRuntimeConfig.AllowInsecureDownloads)
	}
	return result.Ok(m)
}

// GetConfigByName 按名称获取配置
func GetConfigByName(name string) result.R {
	if name == "" {
		return result.Err("配置名称不能为空")
	}
	var cfg model.ViteConfig
	if err := model.DB.Where("name = ?", name).First(&cfg).Error; err != nil {
		return result.Err("配置不存在")
	}
	return result.Ok(cfg)
}

// GetPublicConfigByName 公开读取配置，仅允许安全白名单项。
func GetPublicConfigByName(name string) result.R {
	name = strings.TrimSpace(name)
	if name == "" {
		return result.Err("配置名称不能为空")
	}
	if !isPublicConfigName(name) {
		return result.ErrCode(403, "配置不可公开读取")
	}
	return GetConfigByName(name)
}

// updateOrCreateConfig 存在则更新，否则创建。
// 用 OnConflict 将 check-then-act 收敛为单条原子 upsert，避免并发竞态并把写入错误返回调用方。
func updateOrCreateConfig(name, value string) error {
	return upsertConfig(model.DB, name, value, time.Now().UnixMilli())
}

func upsertConfig(db *gorm.DB, name, value string, now int64) error {
	cfg := model.ViteConfig{Name: name, Value: value, Time: now}
	// name 有 uniqueIndex，冲突时更新 value 与 time
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "time"}),
	}).Create(&cfg).Error
}

// normalizeConfigValue 校验并规范化具备安全含义的系统配置。
// allow_insecure_node_downloads 默认关闭，只接受明确的 true/false，避免拼写错误
// 意外改变节点下载策略；开启后节点密钥与程序可能经明文 HTTP 传输。
// github_download_proxy 默认留空并直连 GitHub；配置后代理可看到并替换下载内容，
// 因此只接受不含凭据、查询参数和片段的 HTTPS 前缀。
func normalizeConfigValue(name, value string) (string, error) {
	switch name {
	case allowInsecureNodeDownloadsConfigName:
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized != "true" && normalized != "false" {
			return "", fmt.Errorf("允许 HTTP 节点安装/升级配置只能为 true 或 false")
		}
		return normalized, nil
	case githubDownloadProxyConfigName:
		return normalizeGitHubDownloadProxy(value)
	default:
		return value, nil
	}
}

func normalizeGitHubDownloadProxy(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", nil
	}
	if len(normalized) > 2048 {
		return "", fmt.Errorf("GitHub 下载代理地址过长")
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("GitHub 下载代理必须是无用户信息的 HTTPS 地址")
	}
	if u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("GitHub 下载代理不能包含查询参数或片段")
	}
	normalizedPath := strings.TrimRight(u.Path, "/")
	cleanedPath := path.Clean(normalizedPath)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if u.RawPath != "" || cleanedPath != normalizedPath {
		return "", fmt.Errorf("GitHub 下载代理路径不能包含编码斜杠、重复分隔符或目录跳转")
	}
	u.Path = normalizedPath
	return u.String(), nil
}

// UpdateConfigs 批量更新配置
func UpdateConfigs(configMap map[string]string) result.R {
	if len(configMap) == 0 {
		return result.Err("配置数据不能为空")
	}
	normalized := make(map[string]string, len(configMap))
	for name, value := range configMap {
		if name == "" {
			continue
		}
		if isR2BackupConfigName(name) {
			return result.Err("Cloudflare R2 备份配置只能通过站点备份中的专用设置修改")
		}
		if name == allowInsecureNodeDownloadsEnvOverrideName {
			return result.Err("环境变量覆盖状态为只读字段，不能直接更新")
		}
		normalizedValue, err := normalizeConfigValue(name, value)
		if err != nil {
			return result.Err(err.Error())
		}
		normalized[name] = normalizedValue
	}
	names := make([]string, 0, len(normalized))
	for name := range normalized {
		names = append(names, name)
	}
	sort.Strings(names)
	now := time.Now().UnixMilli()
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		for _, name := range names {
			if err := upsertConfig(tx, name, normalized[name], now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return result.Err("配置更新失败")
	}
	return result.OkMsg("配置更新成功")
}

// UpdateConfig 更新单个配置
func UpdateConfig(name, value string) result.R {
	if name == "" {
		return result.Err("配置名称不能为空")
	}
	if value == "" {
		return result.Err("配置值不能为空")
	}
	if isR2BackupConfigName(name) {
		return result.Err("Cloudflare R2 备份配置只能通过站点备份中的专用设置修改")
	}
	if name == allowInsecureNodeDownloadsEnvOverrideName {
		return result.Err("环境变量覆盖状态为只读字段，不能直接更新")
	}
	normalizedValue, err := normalizeConfigValue(name, value)
	if err != nil {
		return result.Err(err.Error())
	}
	if err := updateOrCreateConfig(name, normalizedValue); err != nil {
		return result.Err("配置更新失败")
	}
	return result.OkMsg("配置更新成功")
}

// GetConfigValue 内部用：读取配置值，不存在返回空串
func GetConfigValue(name string) string {
	var cfg model.ViteConfig
	if err := model.DB.Where("name = ?", name).First(&cfg).Error; err != nil {
		return ""
	}
	return cfg.Value
}
