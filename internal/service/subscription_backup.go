package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

// MaxProxyNodeBackupSize 默认限制为 4 MiB，覆盖文本协议配置与证书；不接受二进制或整机备份。
const MaxProxyNodeBackupSize = 4 << 20

var proxyNodeBackupSourceHost = regexp.MustCompile(`^node-[A-Za-z0-9_.-]{1,80}$`)
var proxyNodeBackupPort = regexp.MustCompile(`^[0-9]{1,5}$`)
var proxyNodeBackupServiceToken = regexp.MustCompile(`^[A-Za-z0-9_./:@+=-]+$`)
var proxyNodeBackupSNI = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// 只允许脚本实际使用的配置和证书路径。拒绝任意路径、shell/env、系统服务文件，
// 防止恢复时写出配置目录或通过 source/服务启动执行上传的脚本。
var proxyNodeBackupFiles = map[string]bool{
	"cert_domain":      true,
	"certs/server.crt": true, "certs/server.key": true,
	"certs/hy2/server.crt": true, "certs/hy2/server.key": true,
	"certs/tuic/server.crt": true, "certs/tuic/server.key": true,
	"snell.conf": true, "snell-v5.conf": true,
	"snell-shadowtls.conf": true, "snell-v5-shadowtls.conf": true,
	"ss2022-shadowtls-backend.json": true, "Caddyfile": true,
}

var proxyNodeBackupProtocols = map[string]map[string]bool{
	"xray": {
		"vless": true, "vless-xhttp": true, "vless-ws": true, "vmess": true,
		"vmess-ws": true, "vless-vision": true, "trojan": true, "socks": true,
		"ss2022": true, "ss-legacy": true,
	},
	"singbox": {
		"hy2": true, "tuic": true, "snell": true, "snell-v5": true,
		"snell-shadowtls": true, "snell-v5-shadowtls": true,
		"ss2022-shadowtls": true, "anytls": true, "naive": true,
	},
}

// ProxyNodeBackupView 仅展示恢复目标和时间，不返回快照内容；恢复命令仅供管理员和已鉴权节点使用。
type ProxyNodeBackupView struct {
	SourceHost     string   `json:"sourceHost"`
	Server         string   `json:"server"`
	Protocols      []string `json:"protocols"`
	UpdatedTime    int64    `json:"updatedTime"`
	RestoreCommand string   `json:"restoreCommand"`
}

// AuthorizeProxyNodeBackup 在读取大请求体前鉴权；服务方法仍重复校验，避免绕过 HTTP 层后泄露私钥。
func AuthorizeProxyNodeBackup(apiKey string) bool { return isValidSubscriptionAPIKey(apiKey) }

func validateProxyNodeBackup(req dto.ProxyNodeBackupDto) ([]string, error) {
	if req.Version != 1 {
		return nil, fmt.Errorf("不支持的节点备份版本")
	}
	if !proxyNodeBackupSourceHost.MatchString(req.SourceHost) {
		return nil, fmt.Errorf("节点来源标识无效")
	}
	if strings.TrimSpace(req.Server) == "" || len(req.Server) > 255 || strings.ContainsAny(req.Server, "\x00\r\n") {
		return nil, fmt.Errorf("节点备份地址无效")
	}
	var database map[string]json.RawMessage
	if err := json.Unmarshal(req.Database, &database); err != nil || database == nil {
		return nil, fmt.Errorf("节点配置数据库无效")
	}
	if req.Files == nil {
		return nil, fmt.Errorf("节点备份文件集合无效")
	}
	var routingRules []struct {
		Outbound string `json:"outbound"`
	}
	if raw, exists := database["routing_rules"]; exists {
		if err := json.Unmarshal(raw, &routingRules); err != nil {
			return nil, fmt.Errorf("节点备份路由规则无效")
		}
		for _, rule := range routingRules {
			// WARP 依赖外部账户与系统隧道，不在此快照内；禁止恢复时静默改成直连。
			if rule.Outbound == "warp" {
				return nil, fmt.Errorf("当前不支持恢复依赖 WARP 的节点，请使用完整的原系统备份迁移")
			}
		}
	}
	protocols := []string{}
	var xrayProtocols map[string]json.RawMessage
	_ = json.Unmarshal(database["xray"], &xrayProtocols)
	// WS 与主协议共存时使用本机明文回落，证书由主协议决定；独立 WS 才需要自身 TLS 证书。
	hasMaster := xrayProtocols["vless"] != nil || xrayProtocols["vless-vision"] != nil || xrayProtocols["trojan"] != nil
	for core, allowed := range proxyNodeBackupProtocols {
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(database[core], &entries); err != nil || entries == nil {
			return nil, fmt.Errorf("节点配置缺少有效的 %s 协议集合", core)
		}
		for name, raw := range entries {
			if !allowed[name] {
				return nil, fmt.Errorf("节点备份包含不支持的协议")
			}
			var config map[string]json.RawMessage
			if err := json.Unmarshal(raw, &config); err != nil || config == nil {
				return nil, fmt.Errorf("节点协议配置无效")
			}
			if err := validateProxyNodeBackupCredentials(name, config, req.Files, hasMaster); err != nil {
				return nil, err
			}
			// build_config 兼容旧脚本把端口保存为十进制字符串，新数据也可使用 JSON 整数。
			portText := string(config["port"])
			if len(portText) > 0 && portText[0] == '"' {
				if err := json.Unmarshal(config["port"], &portText); err != nil {
					return nil, fmt.Errorf("节点协议端口无效")
				}
			}
			port, err := strconv.Atoi(portText)
			if err != nil || !proxyNodeBackupPort.MatchString(portText) || port < 1 || port > 65535 {
				return nil, fmt.Errorf("节点协议端口无效")
			}
			// OpenRC 服务会把独立协议凭据写入启动参数；这些字段只允许安全单 token，
			// 不接受空白、引号或命令替换，避免备份恢复时生成可执行 shell 内容。
			checks := map[string]*regexp.Regexp{}
			if name == "anytls" {
				checks["password"] = proxyNodeBackupServiceToken
			}
			if strings.Contains(name, "shadowtls") {
				checks["sni"] = proxyNodeBackupSNI
				checks["stls_password"] = proxyNodeBackupServiceToken
				backendField := "snell_backend_port"
				if name == "ss2022-shadowtls" {
					backendField = "ss_backend_port"
				}
				var backend json.Number
				if err := json.Unmarshal(config[backendField], &backend); err != nil {
					return nil, fmt.Errorf("ShadowTLS 后端端口无效")
				}
				port, err := strconv.Atoi(string(backend))
				if err != nil || !proxyNodeBackupPort.MatchString(string(backend)) || port < 1 || port > 65535 {
					return nil, fmt.Errorf("ShadowTLS 后端端口无效")
				}
			}
			for field, pattern := range checks {
				value, ok := proxyNodeBackupScalar(config[field])
				if !ok || !pattern.MatchString(value) {
					return nil, fmt.Errorf("独立协议服务参数不适合安全恢复")
				}
			}
			protocols = append(protocols, name)
		}
	}
	// 不允许空数据库覆盖最后一次可恢复备份，避免新系统首次绑定时丢失旧配置。
	if len(protocols) == 0 {
		return nil, fmt.Errorf("未安装协议，保留原节点备份")
	}
	for name, content := range req.Files {
		if !proxyNodeBackupFiles[name] || strings.ContainsRune(content, '\x00') {
			return nil, fmt.Errorf("节点备份包含不允许的文件")
		}
	}
	sort.Strings(protocols)
	return protocols, nil
}

func validateProxyNodeBackupCredentials(protocol string, config map[string]json.RawMessage, files map[string]string, hasMaster bool) error {
	// 这些参数决定客户端能否沿用原订阅。缺少私钥、凭据或证书时不得覆盖上一次有效快照。
	required := []string{}
	if strings.HasPrefix(protocol, "vless") || strings.HasPrefix(protocol, "vmess") || protocol == "tuic" {
		required = append(required, "uuid")
	}
	if strings.HasPrefix(protocol, "snell") {
		required = append(required, "psk")
	}
	switch protocol {
	case "vless", "vless-xhttp":
		required = append(required, "private_key", "public_key", "short_id", "sni")
	case "trojan", "hy2", "tuic", "anytls", "ss2022", "ss-legacy", "ss2022-shadowtls":
		required = append(required, "password")
	case "socks", "naive":
		required = append(required, "username", "password")
	}
	if protocol == "naive" {
		required = append(required, "domain")
	}
	// 与节点脚本恢复校验保持一致：TLS SNI、WS 路径和 SS 加密方法不可依赖生成器默认值，
	// 否则可能上传成功却无法恢复，或恢复后与客户端已有订阅连接参数不一致。
	switch protocol {
	case "vless-ws", "vmess-ws":
		required = append(required, "sni", "path")
	case "vless-vision", "trojan", "hy2", "tuic":
		required = append(required, "sni")
	case "ss2022", "ss-legacy", "ss2022-shadowtls":
		required = append(required, "method")
	}
	for _, field := range required {
		value, ok := proxyNodeBackupScalar(config[field])
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("节点备份缺少 %s 所需的 %s", protocol, field)
		}
	}
	requiredFiles := []string{}
	switch protocol {
	case "vless-ws", "vmess-ws":
		if !hasMaster {
			requiredFiles = []string{"certs/server.crt", "certs/server.key"}
		}
	case "vless-vision", "trojan":
		requiredFiles = []string{"certs/server.crt", "certs/server.key"}
	case "hy2", "tuic":
		requiredFiles = []string{"certs/" + protocol + "/server.crt", "certs/" + protocol + "/server.key"}
	case "snell", "snell-v5", "snell-shadowtls", "snell-v5-shadowtls":
		requiredFiles = []string{protocol + ".conf"}
	case "ss2022-shadowtls":
		requiredFiles = []string{"ss2022-shadowtls-backend.json"}
	case "naive":
		requiredFiles = []string{"Caddyfile"}
	}
	for _, file := range requiredFiles {
		if strings.TrimSpace(files[file]) == "" {
			return fmt.Errorf("节点备份缺少 %s 所需的 %s", protocol, file)
		}
	}
	return nil
}

// 旧脚本 build_config 会把纯数字凭据（例如全数字 short_id 或密码）保存为 JSON number；
// 兼容这些值的文本形式，同时拒绝 null、布尔值、数组和对象，避免恢复时隐式改变凭据含义。
func proxyNodeBackupScalar(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && len(raw) > 0 && raw[0] == '"' {
		return value, true
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil || number == "" {
		return "", false
	}
	return number.String(), true
}

func SaveProxyNodeBackup(apiKey string, req dto.ProxyNodeBackupDto) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	protocols, err := validateProxyNodeBackup(req)
	if err != nil {
		return result.Err(err.Error())
	}
	raw, err := json.Marshal(req)
	if err != nil || len(raw) > MaxProxyNodeBackupSize {
		return result.Err("节点备份不能超过 4 MiB")
	}
	protocolJSON, _ := json.Marshal(protocols)
	backup := model.ProxyNodeBackup{
		SourceHost: req.SourceHost, Server: req.Server, Protocols: string(protocolJSON),
		Snapshot: string(raw), UpdatedTime: time.Now().UnixMilli(),
	}
	// 单条 upsert 原子替换快照；校验或数据库写入失败时保留上一份，不修改订阅关联。
	if err := model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_host"}},
		DoUpdates: clause.AssignmentColumns([]string{"server", "protocols", "snapshot", "updated_time"}),
	}).Create(&backup).Error; err != nil {
		return result.Err("保存节点恢复备份失败")
	}
	return result.Ok(proxyNodeBackupView(backup))
}

func RestoreProxyNodeBackup(apiKey, sourceHost string) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	if !proxyNodeBackupSourceHost.MatchString(sourceHost) {
		return result.Err("节点来源标识无效")
	}
	var backup model.ProxyNodeBackup
	if err := model.DB.First(&backup, "source_host = ?", sourceHost).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return result.Err("该节点没有恢复备份，请先在原系统使用新版脚本上报")
		}
		return result.Err("读取节点恢复备份失败")
	}
	var req dto.ProxyNodeBackupDto
	if err := json.Unmarshal([]byte(backup.Snapshot), &req); err != nil {
		return result.Err("节点恢复备份已损坏")
	}
	if _, err := validateProxyNodeBackup(req); err != nil || req.SourceHost != sourceHost {
		return result.Err("节点恢复备份已损坏")
	}
	return result.Ok(req)
}

func ListProxyNodeBackups(apiKey string) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	backups, err := proxyNodeBackupViews()
	if err != nil {
		return result.Err("读取节点备份列表失败")
	}
	return result.Ok(backups)
}

func proxyNodeBackupViews() ([]ProxyNodeBackupView, error) {
	var backups []model.ProxyNodeBackup
	// 列表查询不读取私钥快照，避免意外序列化及不必要的敏感数据常驻内存。
	if err := model.DB.Select("source_host", "server", "protocols", "updated_time").Order("updated_time desc, source_host asc").Find(&backups).Error; err != nil {
		return nil, err
	}
	views := make([]ProxyNodeBackupView, 0, len(backups))
	for _, backup := range backups {
		views = append(views, proxyNodeBackupView(backup))
	}
	return views, nil
}

func proxyNodeBackupView(backup model.ProxyNodeBackup) ProxyNodeBackupView {
	protocols := []string{}
	_ = json.Unmarshal([]byte(backup.Protocols), &protocols)
	return ProxyNodeBackupView{
		SourceHost: backup.SourceHost, Server: backup.Server, Protocols: protocols,
		UpdatedTime:    backup.UpdatedTime,
		RestoreCommand: BuildVlessServerRestoreCommand("", "", backup.SourceHost),
	}
}
