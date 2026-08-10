package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nXiaoK/go-panel/internal/model"
)

func RenderSubscription(token, format string) (string, string, error) {
	var profile model.SubscriptionProfile
	if err := model.DB.Where("token = ?", token).First(&profile).Error; err != nil {
		return "", "", fmt.Errorf("订阅不存在")
	}
	if profile.Status != 1 {
		return "", "", fmt.Errorf("订阅已禁用")
	}
	f := normalizeFormat(format)
	if f == "" {
		f = normalizeFormat(profile.DefaultFormat)
	}
	if f == "" {
		f = defaultSubFormat
	}
	nodes := profileRenderNodes(profile.ID)
	switch f {
	case "surge":
		return renderSurge(profile.SurgeTemplate, nodes), "text/plain; charset=utf-8", nil
	case "clash":
		return renderClash(profile.ClashTemplate, nodes), "text/yaml; charset=utf-8", nil
	case "singbox":
		body, err := renderSingbox(profile.SingboxTemplate, nodes)
		if err != nil {
			return "", "", err
		}
		return body, "application/json; charset=utf-8", nil
	case "v2rayn":
		return renderV2rayN(nodes), "text/plain; charset=utf-8", nil
	default:
		return "", "", fmt.Errorf("不支持的订阅格式")
	}
}

func profileRenderNodes(profileID int64) []renderNode {
	var rows []struct {
		model.ProxyNode
		RelationSort int `gorm:"column:relation_sort"`
	}
	query := `
		SELECT pn.*, spn.sort AS relation_sort
		FROM subscription_profile_node spn
		INNER JOIN proxy_node pn ON pn.id = spn.proxy_node_id
		WHERE spn.subscription_id = ? AND pn.status = 1
		ORDER BY spn.sort ASC, pn.sort ASC, pn.created_time DESC`
	model.DB.Raw(query, profileID).Scan(&rows)
	if len(rows) == 0 {
		return []renderNode{}
	}
	out := make([]renderNode, 0, len(rows))
	for _, row := range rows {
		out = append(out, withResolvedAddress(row.ProxyNode))
	}
	return out
}

func withResolvedAddress(node model.ProxyNode) renderNode {
	out := renderNode{ProxyNode: node, Address: node.Server, Port: node.Port}
	if node.ForwardID == nil || *node.ForwardID == 0 {
		return out
	}
	var forward model.Forward
	if err := model.DB.First(&forward, *node.ForwardID).Error; err != nil || forward.Status != forwardStatusActive {
		return out
	}
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil || tunnel.Status != tunnelStatusActive {
		return out
	}
	if strings.TrimSpace(tunnel.InIP) != "" && forward.InPort > 0 {
		out.Address = strings.TrimSpace(tunnel.InIP)
		out.Port = forward.InPort
	}
	return out
}

func renderSurge(template string, nodes []renderNode) string {
	if strings.TrimSpace(template) == "" {
		template = fallbackSurgeTemplate
	}
	lines := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if line := surgeProxyLine(node); line != "" {
			lines = append(lines, line)
		}
	}
	return replaceSurgeProxySection(template, strings.Join(lines, "\n"))
}

func replaceSurgeProxySection(template, proxyBlock string) string {
	if strings.Contains(template, "{{PROXIES}}") {
		return strings.ReplaceAll(template, "{{PROXIES}}", proxyBlock)
	}
	lines := strings.Split(template, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "[Proxy]") {
			start = i
			continue
		}
		if start >= 0 && i > start && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			end = i
			break
		}
	}
	if start == -1 {
		if strings.TrimSpace(template) != "" && !strings.HasSuffix(template, "\n") {
			template += "\n"
		}
		return template + "\n[Proxy]\n" + proxyBlock + "\n"
	}
	out := make([]string, 0, len(lines)+len(strings.Split(proxyBlock, "\n")))
	out = append(out, lines[:start+1]...)
	if proxyBlock != "" {
		out = append(out, strings.Split(proxyBlock, "\n")...)
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

func surgeProxyLine(node renderNode) string {
	name := sanitizeSurgeName(node.Name)
	host := formatHost(node.Address)
	opts := optionsMap(node.Options)
	switch node.Protocol {
	case "snell":
		version := node.SnellVersion
		if version == 0 {
			version = intOption(opts, "version", 5)
		}
		parts := []string{fmt.Sprintf("%s = snell", name), host, strconv.Itoa(node.Port), "psk=" + firstNonEmpty(node.Password, stringOption(opts, "psk")), fmt.Sprintf("version=%d", version)}
		if boolOption(opts, "reuse", true) {
			parts = append(parts, "reuse=true")
		}
		if boolOption(opts, "tfo", false) {
			parts = append(parts, "tfo=true")
		}
		if underlying := stringOption(opts, "underlying-proxy", "underlyingProxy"); underlying != "" {
			parts = append(parts, "underlying-proxy="+underlying)
		}
		return strings.Join(parts, ", ")
	case "vless":
		parts := []string{fmt.Sprintf("%s = vless", name), host, strconv.Itoa(node.Port), "username=" + node.UUID}
		parts = append(parts, surgeTLSOptions(node, opts)...)
		if node.Flow != "" {
			parts = append(parts, "flow="+node.Flow)
		}
		if node.PublicKey != "" {
			parts = append(parts, "reality-public-key="+node.PublicKey)
		}
		if node.ShortID != "" {
			parts = append(parts, "reality-short-id="+node.ShortID)
		}
		if node.Network == "ws" || node.Path != "" {
			parts = append(parts, "ws=true")
			if node.Path != "" {
				parts = append(parts, "ws-path="+node.Path)
			}
			if node.SNI != "" {
				parts = append(parts, "ws-headers=Host:"+node.SNI)
			}
		}
		return strings.Join(nonEmpty(parts), ", ")
	case "vmess":
		parts := []string{fmt.Sprintf("%s = vmess", name), host, strconv.Itoa(node.Port), "username=" + node.UUID}
		parts = append(parts, surgeTLSOptions(node, opts)...)
		if node.Network == "ws" || node.Path != "" {
			parts = append(parts, "ws=true")
			if node.Path != "" {
				parts = append(parts, "ws-path="+node.Path)
			}
			if node.SNI != "" {
				parts = append(parts, "ws-headers=Host:"+node.SNI)
			}
		}
		return strings.Join(nonEmpty(parts), ", ")
	case "trojan":
		parts := []string{fmt.Sprintf("%s = trojan", name), host, strconv.Itoa(node.Port), "password=" + node.Password}
		parts = append(parts, surgeTLSOptions(node, opts)...)
		return strings.Join(nonEmpty(parts), ", ")
	case "ss":
		parts := []string{fmt.Sprintf("%s = ss", name), host, strconv.Itoa(node.Port), "encrypt-method=" + node.Method, "password=" + node.Password}
		// Surge 默认关闭 Shadowsocks 的 UDP Relay；节点声明支持时必须显式开启。
		if node.UDP != 0 {
			parts = append(parts, "udp-relay=true")
		}
		return strings.Join(nonEmpty(parts), ", ")
	case "socks5":
		parts := []string{fmt.Sprintf("%s = socks5", name), host, strconv.Itoa(node.Port)}
		if node.Username != "" {
			parts = append(parts, "username="+node.Username)
		}
		if node.Password != "" {
			parts = append(parts, "password="+node.Password)
		}
		// SOCKS5 的 UDP Associate 是可选能力，Surge 同样要求显式声明。
		if node.UDP != 0 {
			parts = append(parts, "udp-relay=true")
		}
		return strings.Join(nonEmpty(parts), ", ")
	default:
		if node.Link != "" {
			return "# " + name + " = " + node.Link
		}
		return ""
	}
}

func surgeTLSOptions(node renderNode, opts map[string]interface{}) []string {
	parts := []string{}
	if node.Security == "tls" || node.Security == "reality" || node.SNI != "" {
		parts = append(parts, "tls=true")
	}
	if node.SNI != "" {
		parts = append(parts, "sni="+node.SNI)
	}
	if node.AllowInsecure != 0 || boolOption(opts, "skip-cert-verify", false) {
		parts = append(parts, "skip-cert-verify=true")
	}
	if fp := firstNonEmpty(node.Fingerprint, stringOption(opts, "fingerprint", "fp")); fp != "" {
		parts = append(parts, "client-fingerprint="+fp)
	}
	return parts
}

func renderClash(template string, nodes []renderNode) string {
	if strings.TrimSpace(template) == "" || !strings.Contains(template, "proxies:") {
		template = defaultClashTemplate()
	}
	proxies := make([]map[string]interface{}, 0, len(nodes))
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		proxy := clashProxyMap(node)
		if len(proxy) == 0 {
			continue
		}
		name, _ := proxy["name"].(string)
		names = append(names, name)
		proxies = append(proxies, proxy)
	}

	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(template), &root); err != nil || root == nil {
		root = map[string]interface{}{}
	}
	root["proxies"] = proxies
	groups := normalizeClashGroups(root["proxy-groups"], names)
	root["proxy-groups"] = groups
	if _, ok := root["rules"]; !ok {
		root["rules"] = []string{"MATCH,Proxy"}
	}
	if _, ok := root["mixed-port"]; !ok {
		root["mixed-port"] = 7890
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return fallbackClashTemplate
	}
	return string(out)
}

func normalizeClashGroups(raw interface{}, nodeNames []string) []map[string]interface{} {
	var groups []map[string]interface{}
	b, _ := yaml.Marshal(raw)
	_ = yaml.Unmarshal(b, &groups)
	if len(groups) == 0 {
		groups = []map[string]interface{}{
			{"name": "Proxy", "type": "select"},
		}
	}
	nameSet := make(map[string]bool, len(nodeNames))
	for _, name := range nodeNames {
		nameSet[name] = true
	}
	for i := range groups {
		if groups[i]["name"] == nil {
			groups[i]["name"] = fmt.Sprintf("Group-%d", i+1)
		}
		if groups[i]["type"] == nil {
			groups[i]["type"] = "select"
		}
		rawList, _ := groups[i]["proxies"].([]interface{})
		list := make([]interface{}, 0, len(rawList)+len(nodeNames)+1)
		seen := map[string]bool{}
		for _, item := range rawList {
			s := fmt.Sprint(item)
			if s == "" || seen[s] {
				continue
			}
			if s == "DIRECT" || s == "REJECT" || nameSet[s] {
				list = append(list, s)
				seen[s] = true
			}
		}
		for _, name := range nodeNames {
			if !seen[name] {
				list = append(list, name)
				seen[name] = true
			}
		}
		if len(list) == 0 {
			list = append(list, "DIRECT")
		}
		groups[i]["proxies"] = list
	}
	return groups
}

func clashProxyMap(node renderNode) map[string]interface{} {
	opts := optionsMap(node.Options)
	base := map[string]interface{}{
		"name":   node.Name,
		"type":   node.Protocol,
		"server": stripHostBrackets(node.Address),
		"port":   node.Port,
	}
	switch node.Protocol {
	case "vless":
		base["uuid"] = node.UUID
		base["udp"] = node.UDP != 0
		base["tls"] = node.Security == "tls" || node.Security == "reality" || node.SNI != ""
		if node.Security == "reality" {
			base["reality-opts"] = map[string]interface{}{
				"public-key": node.PublicKey,
				"short-id":   node.ShortID,
			}
		}
		if node.SNI != "" {
			base["servername"] = node.SNI
		}
		if node.Flow != "" {
			base["flow"] = node.Flow
		}
		if node.Fingerprint != "" {
			base["client-fingerprint"] = node.Fingerprint
		}
		if node.AllowInsecure != 0 {
			base["skip-cert-verify"] = true
		}
		addClashNetwork(base, node, opts)
	case "vmess":
		base["uuid"] = node.UUID
		base["alterId"] = intOption(opts, "alterId", 0)
		base["cipher"] = firstNonEmpty(node.Method, stringOption(opts, "cipher"), "auto")
		base["udp"] = node.UDP != 0
		if node.Security == "tls" || node.SNI != "" {
			base["tls"] = true
		}
		if node.SNI != "" {
			base["servername"] = node.SNI
		}
		if node.AllowInsecure != 0 {
			base["skip-cert-verify"] = true
		}
		addClashNetwork(base, node, opts)
	case "trojan":
		base["password"] = node.Password
		base["udp"] = node.UDP != 0
		if node.SNI != "" {
			base["sni"] = node.SNI
		}
		if node.AllowInsecure != 0 {
			base["skip-cert-verify"] = true
		}
	case "ss":
		base["cipher"] = node.Method
		base["password"] = node.Password
		base["udp"] = node.UDP != 0
	case "socks5":
		base["type"] = "socks5"
		if node.Username != "" {
			base["username"] = node.Username
		}
		if node.Password != "" {
			base["password"] = node.Password
		}
		base["udp"] = node.UDP != 0
	case "snell":
		base["psk"] = firstNonEmpty(node.Password, stringOption(opts, "psk"))
		version := node.SnellVersion
		if version == 0 {
			version = intOption(opts, "version", 5)
		}
		base["version"] = version
		// Snell v3 及以上支持 UDP；Mihomo/Shadowrocket 需要节点级 udp 标记。
		// Snell 服务端没有 udp=true 配置项，v4/v5 会自动提供 UDP Relay。
		base["udp"] = node.UDP != 0 && version >= 3
	default:
		return map[string]interface{}{}
	}
	for k, v := range opts {
		if _, exists := base[k]; !exists {
			base[k] = v
		}
	}
	return base
}

func addClashNetwork(base map[string]interface{}, node renderNode, opts map[string]interface{}) {
	network := firstNonEmpty(node.Network, stringOption(opts, "network"))
	if network == "" || network == "tcp" {
		return
	}
	base["network"] = network
	if network == "ws" {
		headers := map[string]string{}
		if node.SNI != "" {
			headers["Host"] = node.SNI
		}
		base["ws-opts"] = map[string]interface{}{
			"path":    firstNonEmpty(node.Path, "/"),
			"headers": headers,
		}
	}
	if network == "grpc" {
		base["grpc-opts"] = map[string]interface{}{
			"grpc-service-name": stringOption(opts, "serviceName", "grpc-service-name"),
		}
	}
}

func renderSingbox(template string, nodes []renderNode) (string, error) {
	root, ok := parseSingboxTemplate(template)
	if !ok {
		root, ok = parseSingboxTemplate(defaultSingboxTemplate())
	}
	if !ok {
		return "", fmt.Errorf("默认 sing-box 模板无效")
	}

	rawOutbounds, _ := root["outbounds"].([]interface{})
	preserved := make([]interface{}, 0, len(rawOutbounds)+1)
	usedTags := map[string]bool{"proxy": true}
	dynamicTags := singboxDynamicOutboundTags(rawOutbounds)
	removedTags := map[string]bool{}
	hasDirect := false
	for _, raw := range rawOutbounds {
		outbound, valid := raw.(map[string]interface{})
		if !valid {
			continue
		}
		tag, _ := outbound["tag"].(string)
		tag = strings.TrimSpace(tag)
		outboundType, _ := outbound["type"].(string)
		outboundType = strings.ToLower(strings.TrimSpace(outboundType))
		// proxy 选择器是动态节点占位符。若用户把曾经渲染的订阅粘贴回模板，
		// 其成员用于识别并替换旧动态节点；与该选择器无关的自定义出站保持不变。
		if tag == "proxy" {
			continue
		}
		if dynamicTags[tag] && isSingboxGeneratedProxyType(outboundType) {
			removedTags[tag] = true
			continue
		}
		// direct 与 proxy 是渲染器保留标签；保留其他同名出站会产生重复标签。
		if tag == "direct" {
			if outboundType != "direct" || hasDirect {
				continue
			}
			hasDirect = true
		}
		if tag != "" {
			usedTags[tag] = true
		}
		preserved = append(preserved, outbound)
	}
	if !hasDirect {
		preserved = append(preserved, map[string]interface{}{"type": "direct", "tag": "direct"})
		usedTags["direct"] = true
	}

	resolver := singboxLocalResolverTag(root)
	generated := make([]interface{}, 0, len(nodes))
	generatedTags := make([]interface{}, 0, len(nodes)+1)
	for _, node := range nodes {
		tag := uniqueSingboxTag(node.Name, node.Protocol, usedTags)
		outbound := singboxProxyMap(node, tag, resolver)
		if len(outbound) == 0 {
			continue
		}
		usedTags[tag] = true
		generated = append(generated, outbound)
		generatedTags = append(generatedTags, tag)
	}
	if len(generatedTags) == 0 {
		return "", fmt.Errorf("订阅中没有可供 sing-box 使用的节点")
	}
	selector := map[string]interface{}{
		"type":      "selector",
		"tag":       "proxy",
		"outbounds": generatedTags,
		"default":   generatedTags[0],
	}
	outbounds := make([]interface{}, 0, 1+len(generated)+len(preserved))
	outbounds = append(outbounds, selector)
	outbounds = append(outbounds, generated...)
	outbounds = append(outbounds, preserved...)
	root["outbounds"] = outbounds
	rewriteRemovedSingboxOutboundRefs(root, removedTags)

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}
	return string(out) + "\n", nil
}

func singboxDynamicOutboundTags(outbounds []interface{}) map[string]bool {
	tags := map[string]bool{}
	for _, raw := range outbounds {
		outbound, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := outbound["tag"].(string)
		if strings.TrimSpace(tag) != "proxy" {
			continue
		}
		members, _ := outbound["outbounds"].([]interface{})
		for _, member := range members {
			name, ok := member.(string)
			if ok && name != "" && name != "direct" && name != "proxy" {
				tags[name] = true
			}
		}
	}
	return tags
}

func rewriteRemovedSingboxOutboundRefs(root map[string]interface{}, removed map[string]bool) {
	if len(removed) == 0 {
		return
	}
	var rewrite func(interface{}) interface{}
	rewrite = func(raw interface{}) interface{} {
		switch value := raw.(type) {
		case map[string]interface{}:
			outboundType, _ := value["type"].(string)
			for key, child := range value {
				switch key {
				case "outbound", "detour", "download_detour":
					if tag, ok := child.(string); ok && removed[tag] {
						value[key] = "proxy"
						continue
					}
				case "default":
					if strings.EqualFold(outboundType, "selector") {
						if tag, ok := child.(string); ok && removed[tag] {
							value[key] = "proxy"
							continue
						}
					}
				case "outbounds":
					if members, ok := child.([]interface{}); ok && singboxStringMemberList(members) {
						value[key] = rewriteSingboxOutboundMembers(members, removed)
						continue
					}
				}
				value[key] = rewrite(child)
			}
			return value
		case []interface{}:
			for i := range value {
				value[i] = rewrite(value[i])
			}
			return value
		default:
			return raw
		}
	}
	rewrite(root)
	if route, ok := root["route"].(map[string]interface{}); ok {
		if tag, ok := route["final"].(string); ok && removed[tag] {
			route["final"] = "proxy"
		}
	}
}

func singboxStringMemberList(members []interface{}) bool {
	for _, member := range members {
		if _, ok := member.(string); !ok {
			return false
		}
	}
	return true
}

func rewriteSingboxOutboundMembers(members []interface{}, removed map[string]bool) []interface{} {
	out := make([]interface{}, 0, len(members))
	seen := map[string]bool{}
	for _, member := range members {
		if tag, ok := member.(string); ok {
			if removed[tag] {
				tag = "proxy"
			}
			if seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
			continue
		}
		out = append(out, member)
	}
	return out
}

func parseSingboxTemplate(template string) (map[string]interface{}, bool) {
	if strings.TrimSpace(template) == "" {
		return nil, false
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(template), &root); err != nil || root == nil {
		return nil, false
	}
	if _, ok := root["outbounds"].([]interface{}); !ok {
		return nil, false
	}
	return root, true
}

func isSingboxGeneratedProxyType(outboundType string) bool {
	switch outboundType {
	case "vless", "vmess", "trojan", "shadowsocks", "socks", "socks5", "snell":
		return true
	default:
		return false
	}
}

func uniqueSingboxTag(name, protocol string, used map[string]bool) string {
	base := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name)), " ")
	if base == "" {
		base = firstNonEmpty(normalizeProtocol(protocol), "node")
	}
	tag := base
	for suffix := 2; used[tag]; suffix++ {
		tag = fmt.Sprintf("%s-%d", base, suffix)
	}
	return tag
}

func singboxLocalResolverTag(root map[string]interface{}) string {
	dns, _ := root["dns"].(map[string]interface{})
	servers, _ := dns["servers"].([]interface{})
	fallback := ""
	for _, raw := range servers {
		server, ok := raw.(map[string]interface{})
		serverType, _ := server["type"].(string)
		if !ok || !strings.EqualFold(serverType, "local") {
			continue
		}
		tag, _ := server["tag"].(string)
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if tag == "dns-local" {
			return tag
		}
		if fallback == "" {
			fallback = tag
		}
	}
	return fallback
}

func singboxProxyMap(node renderNode, tag, resolver string) map[string]interface{} {
	server := stripHostBrackets(node.Address)
	if server == "" || node.Port <= 0 || node.Port > 65535 {
		return nil
	}
	opts := optionsMap(node.Options)
	base := map[string]interface{}{
		"tag":         tag,
		"server":      server,
		"server_port": node.Port,
	}
	if resolver != "" && net.ParseIP(server) == nil {
		base["domain_resolver"] = resolver
	}

	switch normalizeProtocol(node.Protocol) {
	case "vless":
		if strings.TrimSpace(node.UUID) == "" {
			return nil
		}
		base["type"] = "vless"
		base["uuid"] = node.UUID
		if node.Flow != "" {
			base["flow"] = node.Flow
		}
		if !addSingboxTLS(base, node, opts, false) || !addSingboxTransport(base, node, opts) {
			return nil
		}
	case "vmess":
		if strings.TrimSpace(node.UUID) == "" {
			return nil
		}
		base["type"] = "vmess"
		base["uuid"] = node.UUID
		base["security"] = firstNonEmpty(node.Method, stringOption(opts, "security", "cipher"), "auto")
		if alterID := intOption(opts, "alterId", intOption(opts, "alter_id", 0)); alterID != 0 {
			base["alter_id"] = alterID
		}
		if !addSingboxTLS(base, node, opts, false) || !addSingboxTransport(base, node, opts) {
			return nil
		}
	case "trojan":
		if strings.TrimSpace(node.Password) == "" {
			return nil
		}
		base["type"] = "trojan"
		base["password"] = node.Password
		if !addSingboxTLS(base, node, opts, true) || !addSingboxTransport(base, node, opts) {
			return nil
		}
	case "ss":
		method := firstNonEmpty(node.Method, stringOption(opts, "method", "cipher"))
		password := firstNonEmpty(node.Password, stringOption(opts, "password"))
		if method == "" || password == "" {
			return nil
		}
		base["type"] = "shadowsocks"
		base["method"] = method
		base["password"] = password
		if plugin := stringOption(opts, "plugin"); plugin != "" {
			base["plugin"] = plugin
		}
		if pluginOpts := stringOption(opts, "plugin_opts", "plugin-opts"); pluginOpts != "" {
			base["plugin_opts"] = pluginOpts
		}
	case "socks5":
		base["type"] = "socks"
		base["version"] = "5"
		if node.Username != "" {
			base["username"] = node.Username
		}
		if node.Password != "" {
			base["password"] = node.Password
		}
	default:
		// sing-box 1.13 没有 Snell 出站；不支持的协议直接跳过，避免生成无法导入的配置。
		return nil
	}
	return base
}

func addSingboxTLS(base map[string]interface{}, node renderNode, opts map[string]interface{}, required bool) bool {
	security := strings.ToLower(strings.TrimSpace(node.Security))
	if !required && security != "tls" && security != "reality" {
		return true
	}
	tls := map[string]interface{}{"enabled": true}
	if serverName := firstNonEmpty(node.SNI, stringOption(opts, "server_name", "servername", "sni")); serverName != "" {
		tls["server_name"] = serverName
	}
	if node.AllowInsecure != 0 || boolOption(opts, "insecure", boolOption(opts, "skip-cert-verify", false)) {
		tls["insecure"] = true
	}
	if fingerprint := firstNonEmpty(node.Fingerprint, stringOption(opts, "fingerprint", "fp")); fingerprint != "" {
		tls["utls"] = map[string]interface{}{
			"enabled":     true,
			"fingerprint": fingerprint,
		}
	}
	if security == "reality" {
		publicKey := firstNonEmpty(node.PublicKey, stringOption(opts, "public_key", "public-key", "pbk"))
		if publicKey == "" {
			return false
		}
		reality := map[string]interface{}{
			"enabled":    true,
			"public_key": publicKey,
		}
		if shortID := firstNonEmpty(node.ShortID, stringOption(opts, "short_id", "short-id", "sid")); shortID != "" {
			reality["short_id"] = shortID
		}
		tls["reality"] = reality
	}
	base["tls"] = tls
	return true
}

func addSingboxTransport(base map[string]interface{}, node renderNode, opts map[string]interface{}) bool {
	network := strings.ToLower(firstNonEmpty(node.Network, stringOption(opts, "network")))
	switch network {
	case "", "tcp":
		return true
	case "ws", "websocket":
		transport := map[string]interface{}{
			"type": "ws",
			"path": firstNonEmpty(node.Path, stringOption(opts, "path"), "/"),
		}
		if host := firstNonEmpty(stringOption(opts, "host"), node.SNI); host != "" {
			transport["headers"] = map[string]interface{}{"Host": host}
		}
		base["transport"] = transport
		return true
	case "grpc":
		transport := map[string]interface{}{"type": "grpc"}
		if serviceName := stringOption(opts, "service_name", "serviceName", "grpc-service-name"); serviceName != "" {
			transport["service_name"] = serviceName
		}
		base["transport"] = transport
		return true
	default:
		// sing-box 1.13 不支持 XHTTP。未知传输不能静默降级成普通 TCP，
		// 否则选择器可能默认选中一个实际无法连接的节点。
		return false
	}
}

func renderV2rayN(nodes []renderNode) string {
	links := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if link := v2rayNShareLink(node); link != "" {
			links = append(links, link)
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

func v2rayNShareLink(node renderNode) string {
	switch normalizeProtocol(node.Protocol) {
	case "vless":
		return v2rayNVLESSLink(node)
	case "vmess":
		return v2rayNVMessLink(node)
	case "trojan":
		return v2rayNTrojanLink(node)
	case "ss":
		return v2rayNSSLink(node)
	case "socks5":
		return v2rayNSocksLink(node)
	default:
		return ""
	}
}

func v2rayNVLESSLink(node renderNode) string {
	if strings.TrimSpace(node.UUID) == "" {
		return ""
	}
	opts := optionsMap(node.Options)
	query := url.Values{}
	query.Set("encryption", firstNonEmpty(stringOption(opts, "encryption"), "none"))
	addQueryIfNotEmpty(query, "security", node.Security)
	addQueryIfNotEmpty(query, "type", firstNonEmpty(node.Network, stringOption(opts, "network")))
	addQueryIfNotEmpty(query, "sni", node.SNI)
	addQueryIfNotEmpty(query, "host", stringOption(opts, "host"))
	addQueryIfNotEmpty(query, "path", node.Path)
	addQueryIfNotEmpty(query, "flow", node.Flow)
	addQueryIfNotEmpty(query, "pbk", node.PublicKey)
	addQueryIfNotEmpty(query, "sid", node.ShortID)
	addQueryIfNotEmpty(query, "fp", firstNonEmpty(node.Fingerprint, stringOption(opts, "fingerprint", "fp")))
	// VLESS 通过现有流式连接承载 UDP，不会额外监听同端口 UDP。
	// 显式写入 none 可让支持该 URI 扩展的客户端保留“启用 UDP、使用原始包地址”语义。
	if node.UDP != 0 {
		query.Set("packetEncoding", "none")
	}
	if node.AllowInsecure != 0 {
		query.Set("allowInsecure", "1")
	}
	return (&url.URL{
		Scheme:   "vless",
		User:     url.User(node.UUID),
		Host:     v2rayNHostPort(node),
		RawQuery: query.Encode(),
		Fragment: node.Name,
	}).String()
}

func v2rayNVMessLink(node renderNode) string {
	if strings.TrimSpace(node.UUID) == "" {
		return ""
	}
	opts := optionsMap(node.Options)
	network := firstNonEmpty(node.Network, stringOption(opts, "network"), "tcp")
	tls := ""
	if node.Security == "tls" || node.Security == "reality" || node.SNI != "" {
		tls = node.Security
		if tls == "" || tls == "reality" {
			tls = "tls"
		}
	}
	payload := map[string]string{
		"v":    "2",
		"ps":   node.Name,
		"add":  stripHostBrackets(node.Address),
		"port": strconv.Itoa(node.Port),
		"id":   node.UUID,
		"aid":  firstNonEmpty(stringOption(opts, "alterId", "aid"), "0"),
		"scy":  firstNonEmpty(node.Method, stringOption(opts, "cipher", "scy"), "auto"),
		"net":  network,
		"type": firstNonEmpty(stringOption(opts, "type", "headerType"), "none"),
		"host": firstNonEmpty(stringOption(opts, "host"), node.SNI),
		"path": node.Path,
		"tls":  tls,
		"sni":  node.SNI,
		"alpn": stringOption(opts, "alpn"),
		"fp":   firstNonEmpty(node.Fingerprint, stringOption(opts, "fingerprint", "fp")),
	}
	b, _ := json.Marshal(payload)
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

func v2rayNTrojanLink(node renderNode) string {
	if strings.TrimSpace(node.Password) == "" {
		return ""
	}
	opts := optionsMap(node.Options)
	query := url.Values{}
	addQueryIfNotEmpty(query, "security", firstNonEmpty(node.Security, "tls"))
	addQueryIfNotEmpty(query, "type", firstNonEmpty(node.Network, stringOption(opts, "network")))
	addQueryIfNotEmpty(query, "sni", node.SNI)
	addQueryIfNotEmpty(query, "host", stringOption(opts, "host"))
	addQueryIfNotEmpty(query, "path", node.Path)
	if node.AllowInsecure != 0 {
		query.Set("allowInsecure", "1")
	}
	return (&url.URL{
		Scheme:   "trojan",
		User:     url.User(node.Password),
		Host:     v2rayNHostPort(node),
		RawQuery: query.Encode(),
		Fragment: node.Name,
	}).String()
}

func v2rayNSSLink(node renderNode) string {
	if strings.TrimSpace(node.Method) == "" || strings.TrimSpace(node.Password) == "" {
		return ""
	}
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(node.Method + ":" + node.Password))
	return "ss://" + userInfo + "@" + v2rayNHostPort(node) + "#" + url.QueryEscape(node.Name)
}

func v2rayNSocksLink(node renderNode) string {
	target := v2rayNHostPort(node)
	if node.Username != "" || node.Password != "" {
		target = url.UserPassword(node.Username, node.Password).String() + "@" + target
	}
	return "socks://" + base64.RawURLEncoding.EncodeToString([]byte(target)) + "#" + url.QueryEscape(node.Name)
}

func v2rayNHostPort(node renderNode) string {
	return net.JoinHostPort(stripHostBrackets(node.Address), strconv.Itoa(node.Port))
}

func addQueryIfNotEmpty(query url.Values, key, value string) {
	if strings.TrimSpace(value) != "" {
		query.Set(key, strings.TrimSpace(value))
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func sanitizeSurgeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Proxy"
	}
	name = strings.ReplaceAll(name, "\n", " ")
	name = strings.ReplaceAll(name, "=", "-")
	return name
}

func formatHost(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host
	}
	if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func stripHostBrackets(host string) string {
	return strings.Trim(strings.TrimSpace(host), "[]")
}
