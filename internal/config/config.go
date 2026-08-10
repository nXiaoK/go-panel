package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 运行配置，均可通过环境变量覆盖
type Config struct {
	// 监听地址，默认 127.0.0.1:6365（与 Java 版端口一致）
	ListenAddr string
	// JWT 签名密钥
	JwtSecret string
	// JWT_SECRET 是否来自持久环境配置；仅持久密钥可用于加密需要跨重启读取的 R2 凭据
	JwtSecretPersistent bool
	// 启动时创建管理员所使用的用户名
	AdminUsername string
	// 启动时创建或轮换管理员所使用的密码
	AdminPassword string
	// 是否允许通过明文 HTTP 下载节点程序
	AllowInsecureNodeDownloads bool
	// 是否临时允许不具备幂等保障的旧版 nft 流量上报
	AllowLegacyNftReports bool
	// 可信反向代理 CIDR 列表
	TrustedProxies []string
	// SQLite 数据库文件路径
	DBPath string
	// CORS 允许的来源（逗号分隔），空值表示 *
	CorsAllowOrigin string
	// 是否允许后端访问 GitHub Releases 检查稳定版更新；失败不会影响面板运行
	UpdateCheckEnabled bool
	// 更新来源仓库，格式必须为 owner/repository
	UpdateRepository string
	// 成功检查结果的缓存时间，避免频繁请求 GitHub 公共 API
	UpdateCheckInterval time.Duration
	// 可选更新侧车的内部 HTTP 地址；为空时界面仅显示手动升级命令
	UpdateTriggerURL string
	// 调用更新侧车所需的 Bearer Token；不得写入仓库或日志
	UpdateTriggerToken string
	// 当前部署使用的镜像标签；只有 latest 会启用跨稳定版本的一键更新
	UpdateImageTag string
}

// DefaultJwtSecret is the historical insecure JWT fallback and must be rejected remotely.
const DefaultJwtSecret = "change-me-please"

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(key)))
	return v
}

func getenvBoolDefault(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return value
}

func getenvDuration(key string, def, minimum, maximum time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return def
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func getenvList(key string) []string {
	var values []string
	for _, value := range strings.Split(os.Getenv(key), ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// Load 从环境变量读取配置
func Load() *Config {
	jwtSecret := os.Getenv("JWT_SECRET")
	return &Config{
		ListenAddr:                 getenv("LISTEN_ADDR", "127.0.0.1:6365"),
		JwtSecret:                  jwtSecret,
		JwtSecretPersistent:        strings.TrimSpace(jwtSecret) != "",
		AdminUsername:              getenv("ADMIN_USERNAME", "admin_user"),
		AdminPassword:              os.Getenv("ADMIN_PASSWORD"),
		AllowInsecureNodeDownloads: getenvBool("ALLOW_INSECURE_NODE_DOWNLOADS"),
		AllowLegacyNftReports:      getenvBool("ALLOW_LEGACY_NFT_REPORTS"),
		TrustedProxies:             getenvList("TRUSTED_PROXIES"),
		DBPath:                     getenv("DB_PATH", "data/flux-panel.db"),
		CorsAllowOrigin:            getenv("CORS_ALLOW_ORIGIN", ""),
		UpdateCheckEnabled:         getenvBoolDefault("UPDATE_CHECK_ENABLED", true),
		UpdateRepository:           getenv("UPDATE_REPOSITORY", "nXiaoK/go-panel"),
		UpdateCheckInterval:        getenvDuration("UPDATE_CHECK_INTERVAL", 6*time.Hour, 5*time.Minute, 7*24*time.Hour),
		UpdateTriggerURL:           strings.TrimSpace(os.Getenv("UPDATE_TRIGGER_URL")),
		UpdateTriggerToken:         strings.TrimSpace(os.Getenv("UPDATE_TRIGGER_TOKEN")),
		UpdateImageTag:             getenv("UPDATE_IMAGE_TAG", "latest"),
	}
}
