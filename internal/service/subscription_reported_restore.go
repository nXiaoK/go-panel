package service

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

// ReportedNodeRestoreView 只暴露兼容性和操作结果，不暴露用于恢复的密码、UUID 或新生成私钥。
type ReportedNodeRestoreView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
}

type ReportedHostRestoreView struct {
	SourceHost     string                    `json:"sourceHost"`
	Server         string                    `json:"server"`
	Nodes          []ReportedNodeRestoreView `json:"nodes"`
	RestoreCommand string                    `json:"restoreCommand"`
}

// ReportedNodeRestoreResult 是临时重建快照。查询不会写入备份或修改原节点，
// 只有脚本部署并健康检查成功后的原身份上报，才更新订阅中的新 Reality 公钥等字段。
type ReportedNodeRestoreResult struct {
	Snapshot dto.ProxyNodeBackupDto `json:"snapshot"`
	Notices  []string               `json:"notices"`
}

type reportedRestoreOptions struct {
	Source           string          `json:"source"`
	SourceHost       string          `json:"sourceHost"`
	OriginalProtocol string          `json:"originalProtocol"`
	OriginalPort     json.RawMessage `json:"originalPort"`
}

type reportedRestoreNode struct {
	node   model.ProxyNode
	view   ReportedNodeRestoreView
	config map[string]any
	files  map[string]string
}

type reportedRestoreGroup struct {
	view  ReportedHostRestoreView
	nodes []reportedRestoreNode
}

func BuildVlessServerReportedRestoreCommand(panelURL, apiKey, sourceHost string) string {
	if !proxyNodeBackupSourceHost.MatchString(sourceHost) {
		return ""
	}
	return buildVlessServerCommand(panelURL, apiKey, "--flux-panel-restore-reported", sourceHost)
}

func ListReportedNodeRestores(apiKey string) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	views, err := reportedNodeRestoreViews()
	if err != nil {
		return result.Err("读取旧上报恢复列表失败")
	}
	return result.Ok(views)
}

func reportedNodeRestoreViews() ([]ReportedHostRestoreView, error) {
	groups, err := reportedNodeRestoreGroups()
	if err != nil {
		return nil, err
	}
	views := make([]ReportedHostRestoreView, 0, len(groups))
	for _, group := range groups {
		views = append(views, group.view)
	}
	return views, nil
}

func reportedNodeRestoreGroups() ([]reportedRestoreGroup, error) {
	var backups []model.ProxyNodeBackup
	if err := model.DB.Select("source_host").Find(&backups).Error; err != nil {
		return nil, err
	}
	complete := map[string]bool{}
	for _, backup := range backups {
		complete[backup.SourceHost] = true
	}
	var nodes []model.ProxyNode
	// 派生中转节点携带客户端参数副本，不能当作原服务器的独立协议重新部署。
	if err := model.DB.Where("source_proxy_node_id IS NULL AND (relay_mode IS NULL OR relay_mode <> ?)", "append").Order("id asc").Find(&nodes).Error; err != nil {
		return nil, err
	}
	groupMap := map[string]*reportedRestoreGroup{}
	order := []string{}
	for _, node := range nodes {
		var options reportedRestoreOptions
		if json.Unmarshal([]byte(node.Options), &options) != nil || options.Source != "vless-server.sh" {
			continue
		}
		if complete[options.SourceHost] {
			continue
		}
		candidate := planReportedNodeRestore(node, options)
		validSource := proxyNodeBackupSourceHost.MatchString(options.SourceHost) && options.OriginalProtocol != "" && node.ExternalID == options.SourceHost+"-"+options.OriginalProtocol
		key, source := options.SourceHost, options.SourceHost
		if !validSource {
			// 来源不明的记录逐条展示，不按 IP 聚合，更不能生成写入另一主机身份的恢复命令。
			key, source = fmt.Sprintf("unresolved:%d", node.ID), ""
			candidate.view.Action, candidate.view.Reason = "unsupported", "缺少可验证的原节点身份，不能按 IP 自动合并恢复"
		}
		group := groupMap[key]
		if group == nil {
			group = &reportedRestoreGroup{view: ReportedHostRestoreView{SourceHost: source, Server: node.Server, Nodes: []ReportedNodeRestoreView{}}}
			groupMap[key] = group
			order = append(order, key)
		}
		group.nodes = append(group.nodes, candidate)
	}
	groups := make([]reportedRestoreGroup, 0, len(order))
	for _, key := range order {
		group := groupMap[key]
		validateReportedRestoreGroup(group)
		inconsistent := false
		for _, node := range group.nodes {
			if node.node.Server != group.view.Server {
				inconsistent = true
			}
		}
		for i := range group.nodes {
			if inconsistent {
				group.nodes[i].view.Action, group.nodes[i].view.Reason = "unsupported", "同一来源存在不同服务器地址，请先核实原节点记录"
			}
			view := group.nodes[i].view
			group.view.Nodes = append(group.view.Nodes, view)
			if view.Action != "unsupported" && group.view.SourceHost != "" {
				group.view.RestoreCommand = BuildVlessServerReportedRestoreCommand("", "", group.view.SourceHost)
			}
		}
		groups = append(groups, *group)
	}
	return groups, nil
}

func validateReportedRestoreGroup(group *reportedRestoreGroup) {
	// 旧上报没有保存完整回落拓扑。当前重建仅支持独立 TCP 监听，不能把内网 WS 当成公网入口。
	hasMaster := false
	for _, plan := range group.nodes {
		if plan.view.Action != "unsupported" && (plan.view.Protocol == "vless" || plan.view.Protocol == "vless-vision" || plan.view.Protocol == "trojan") {
			hasMaster = true
		}
	}
	ports := map[int][]int{}
	for i := range group.nodes {
		plan := &group.nodes[i]
		if plan.view.Action == "unsupported" {
			continue
		}
		if hasMaster && (plan.view.Protocol == "vless-ws" || plan.view.Protocol == "vmess-ws") {
			plan.view.Action, plan.view.Reason = "unsupported", "WS 与主协议共存时依赖回落拓扑，旧上报无法完整重建，请使用完整备份"
			continue
		}
		ports[plan.view.Port] = append(ports[plan.view.Port], i)
	}
	for _, indices := range ports {
		if len(indices) < 2 {
			continue
		}
		for _, i := range indices {
			group.nodes[i].view.Action, group.nodes[i].view.Reason = "unsupported", "多个协议使用同一 TCP 监听端口，旧上报缺少共享入口配置，不能安全重建"
		}
	}
}

func planReportedNodeRestore(node model.ProxyNode, options reportedRestoreOptions) reportedRestoreNode {
	protocol := options.OriginalProtocol
	plan := reportedRestoreNode{node: node, view: ReportedNodeRestoreView{ID: node.ID, Name: node.Name, Protocol: protocol, Port: node.Port, Action: "unsupported"}, files: map[string]string{}}
	if protocol == "" {
		plan.view.Protocol = node.Protocol
	}
	deny := func(reason string) reportedRestoreNode { plan.view.Reason = reason; return plan }
	if normalizeProtocol(protocol) != normalizeProtocol(node.Protocol) {
		return deny("上报协议与原安装协议不一致，不能可靠重建")
	}
	port := node.Port
	if len(options.OriginalPort) > 0 {
		value, ok := proxyNodeBackupScalar(options.OriginalPort)
		parsed, err := strconv.Atoi(value)
		if !ok || err != nil || !proxyNodeBackupPort.MatchString(value) || parsed < 1 || parsed > 65535 {
			return deny("原服务端监听端口无效")
		}
		port = parsed
	} else if protocol == "vless-ws" || protocol == "vmess-ws" {
		return deny("旧记录缺少 WS 实际监听端口，不能推断回落配置")
	}
	plan.view.Port = port
	if port != node.Port {
		return deny("原监听端口与当前订阅端口不一致，可能依赖回落或端口映射，请使用完整备份")
	}
	if port < 1 || port > 65535 || strings.TrimSpace(node.Server) == "" || strings.ContainsAny(node.Server, "\x00\r\n") {
		return deny("节点地址或端口无效")
	}
	config := map[string]any{"port": port}
	plan.config = config
	fields := map[string]string{}
	tls := false
	switch protocol {
	case "vless", "vless-xhttp":
		if node.Security != "reality" || (node.Network != "" && node.Network != "tcp" && !(protocol == "vless-xhttp" && node.Network == "xhttp")) {
			return deny("旧记录不是可重建的 Reality 传输配置")
		}
		fields = map[string]string{"uuid": node.UUID, "sni": node.SNI}
		config["short_id"] = node.ShortID
		if protocol == "vless-xhttp" {
			config["path"] = node.Path
			if node.Path == "" {
				config["path"] = "/"
			}
		}
	case "vmess":
		if (node.Network != "" && node.Network != "tcp") || (node.Security != "" && node.Security != "none") {
			return deny("仅能从旧记录重建 VMess TCP，其他传输缺少服务端配置")
		}
		fields = map[string]string{"uuid": node.UUID}
	case "ss2022", "ss-legacy":
		if (node.Network != "" && node.Network != "tcp") || (node.Security != "" && node.Security != "none") {
			return deny("旧记录包含额外传输或安全层，缺少完整服务端配置")
		}
		fields = map[string]string{"password": node.Password, "method": node.Method}
	case "socks":
		if (node.Network != "" && node.Network != "tcp") || (node.Security != "" && node.Security != "none") {
			return deny("旧记录包含额外传输或安全层，缺少完整服务端配置")
		}
		fields = map[string]string{"username": node.Username, "password": node.Password}
	case "snell", "snell-v5":
		if strings.IndexFunc(node.Password, unicode.IsControl) >= 0 {
			return deny("Snell PSK 含控制字符，不能安全写入配置文件")
		}
		fields = map[string]string{"psk": node.Password}
		version := 4
		if protocol == "snell-v5" {
			version = 5
		}
		if node.SnellVersion != 0 && node.SnellVersion != version {
			return deny("Snell 版本与原协议不一致")
		}
		config["version"] = version
		versionLine := ""
		if version == 5 {
			versionLine = "version = 5\n"
		}
		plan.files[protocol+".conf"] = fmt.Sprintf("# 从旧上报记录恢复：保留原端口及 PSK，仅接受可信面板下发。\n[snell-server]\nlisten = [::]:%d\npsk = %s\n%sipv6 = true\nobfs = off\n", port, node.Password, versionLine)
	case "vless-vision", "vless-ws", "vmess-ws", "trojan":
		// 旧上报不含证书私钥，仅在原订阅已明确允许不校验证书时生成自签证书。
		// 原本严格验证 TLS 的配置保持拒绝，不能为恢复方便偷偷降低安全策略。
		if node.AllowInsecure != 1 || node.Security != "tls" {
			return deny("缺少原 TLS 证书且原订阅未允许自签证书，需提供证书或完整备份")
		}
		tls = true
		fields = map[string]string{"sni": node.SNI}
		if protocol == "trojan" {
			fields["password"] = node.Password
		} else {
			fields["uuid"] = node.UUID
		}
		if protocol == "vless-ws" || protocol == "vmess-ws" {
			fields["path"] = node.Path
		}
	default:
		return deny("旧上报未保存该协议所需的全部服务端参数，请使用完整备份恢复")
	}
	for field, value := range fields {
		if strings.TrimSpace(value) == "" {
			return deny("旧上报缺少必要配置：" + field)
		}
		config[field] = value
	}
	plan.view.Action, plan.view.Reason = "preserve", "保留原端口与连接凭据"
	if protocol == "vless" || protocol == "vless-xhttp" {
		plan.view.Action, plan.view.Reason = "regenerate", "原 Reality 私钥未保存，将生成新密钥；恢复后请刷新客户端订阅"
	} else if tls {
		plan.view.Action, plan.view.Reason = "regenerate", "生成新自签证书，保留原凭据及原有跳过证书验证设置"
	}
	return plan
}

func RestoreReportedProxyNodes(apiKey, sourceHost string) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	if !proxyNodeBackupSourceHost.MatchString(sourceHost) {
		return result.Err("节点来源标识无效")
	}
	var count int64
	if err := model.DB.Model(&model.ProxyNodeBackup{}).Where("source_host = ?", sourceHost).Count(&count).Error; err != nil {
		return result.Err("读取节点恢复备份失败")
	}
	if count > 0 {
		return result.Err("该节点已有完整恢复备份，请使用完整备份恢复以保留原私钥")
	}
	groups, err := reportedNodeRestoreGroups()
	if err != nil {
		return result.Err("读取旧上报节点失败")
	}
	for _, group := range groups {
		if group.view.SourceHost != sourceHost {
			continue
		}
		if group.view.RestoreCommand == "" {
			return result.Err("该来源没有可从旧上报恢复的协议，请检查恢复列表中的原因")
		}
		response, err := buildReportedNodeSnapshot(group)
		if err != nil {
			return result.Err("重建节点配置失败：" + err.Error())
		}
		return result.Ok(response)
	}
	return result.Err("找不到具备可靠原身份的旧上报记录")
}

func buildReportedNodeSnapshot(group reportedRestoreGroup) (ReportedNodeRestoreResult, error) {
	xray, singbox := map[string]any{}, map[string]any{}
	files := map[string]string{}
	notices := []string{"根据旧上报重建服务端配置；原订阅绑定保留，未保存的分流和外部服务配置不会恢复。"}
	tlsNames := []string{}
	for _, plan := range group.nodes {
		if plan.view.Action == "unsupported" {
			notices = append(notices, plan.view.Name+"：已跳过，"+plan.view.Reason)
			continue
		}
		protocol := plan.view.Protocol
		if protocol == "vless" || protocol == "vless-xhttp" {
			key, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				return ReportedNodeRestoreResult{}, fmt.Errorf("生成 Reality 密钥失败")
			}
			// 与 Xray 官方 x25519 命令的私钥导出保持一致：先 clamp，再使用无 padding 的 URL-safe Base64。
			// https://github.com/XTLS/Xray-core/blob/main/main/commands/all/curve25519.go
			private := key.Bytes()
			private[0] &= 248
			private[31] &= 127
			private[31] |= 64
			key, err = ecdh.X25519().NewPrivateKey(private)
			if err != nil {
				return ReportedNodeRestoreResult{}, fmt.Errorf("生成 Reality 密钥失败")
			}
			plan.config["private_key"] = base64.RawURLEncoding.EncodeToString(private)
			plan.config["public_key"] = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
			if plan.node.ShortID == "" {
				id := make([]byte, 4)
				if _, err := rand.Read(id); err != nil {
					return ReportedNodeRestoreResult{}, fmt.Errorf("生成 Reality short ID 失败")
				}
				plan.config["short_id"] = hex.EncodeToString(id)
			}
		}
		if plan.node.Security == "tls" {
			tlsNames = append(tlsNames, plan.node.SNI)
		}
		if protocol == "snell" || protocol == "snell-v5" {
			singbox[protocol] = plan.config
		} else {
			xray[protocol] = plan.config
		}
		for name, content := range plan.files {
			files[name] = content
		}
		notices = append(notices, plan.view.Name+"："+plan.view.Reason)
	}
	if len(tlsNames) > 0 {
		cert, key, err := reportedRestoreCertificate(tlsNames)
		if err != nil {
			return ReportedNodeRestoreResult{}, fmt.Errorf("生成恢复证书失败")
		}
		files["certs/server.crt"], files["certs/server.key"] = cert, key
	}
	database, err := json.Marshal(map[string]any{"version": "4.0.0", "xray": xray, "singbox": singbox})
	if err != nil {
		return ReportedNodeRestoreResult{}, err
	}
	snapshot := dto.ProxyNodeBackupDto{Version: 1, SourceHost: group.view.SourceHost, Server: group.view.Server, Database: database, Files: files}
	if _, err := validateProxyNodeBackup(snapshot); err != nil {
		return ReportedNodeRestoreResult{}, err
	}
	return ReportedNodeRestoreResult{Snapshot: snapshot, Notices: notices}, nil
}

func reportedRestoreCertificate(names []string) (string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	// 自签证书仅用于旧订阅已 AllowInsecure 的协议；默认有效期一年，不建立新的公开信任链。
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "go-panel restored node"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	sort.Strings(names)
	for _, name := range uniqueStrings(names) {
		if ip := net.ParseIP(name); ip != nil {
			cert.IPAddresses = append(cert.IPAddresses, ip)
		} else {
			cert.DNSNames = append(cert.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}
