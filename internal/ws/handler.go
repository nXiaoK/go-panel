package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/middleware"
	"github.com/nXiaoK/go-panel/internal/model"
)

const (
	// pongWait 读取超时时间（等待 pong 响应）
	pongWait = 90 * time.Second
	// pingPeriod 发送 ping 的间隔（必须小于 pongWait）
	pingPeriod = (pongWait * 9) / 10
	// maxWebSocketMessageSize bounds both node and administrator messages.
	maxWebSocketMessageSize int64 = 1 << 20
	// maxReportedPanelBaseURLLength 限制节点上报的历史面板基址长度，避免异常握手参数写入超长配置。
	maxReportedPanelBaseURLLength = 2048
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkOrigin,
}

var allowedOrigins string

// SetAllowedOrigins configures browser WebSocket origin checks.
// Empty or "*" keeps the historical allow-all behavior.
func SetAllowedOrigins(origins string) {
	allowedOrigins = origins
}

func checkOrigin(r *http.Request) bool {
	if allowedOrigins == "" || allowedOrigins == "*" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// 非浏览器客户端（如节点 agent）不发 Origin，靠 secret/JWT 鉴权，放行
		return true
	}
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// maskSecretForLog 脱敏 secret，仅显示前 8 位，便于辨识又不泄露全量。
func maskSecretForLog(secret string) string {
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:8] + "..."
}

// adminOriginAllowed 管理员 WebSocket 连接的额外 Origin 校验。
// 与 checkOrigin 不同：配置了白名单时，浏览器请求（带 Origin）必须严格匹配，
// 空 Origin（非浏览器）也放行（靠 JWT 鉴权）。用于缓解 CSWSH（跨站 WS 劫持）。
func adminOriginAllowed(r *http.Request) bool {
	if allowedOrigins == "" || allowedOrigins == "*" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // 非浏览器，靠 JWT
	}
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// commandResponse 节点命令响应报文（包含 requestId）
type commandResponse struct {
	RequestId string          `json:"requestId"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
}

// HandleSystemInfo 处理 /system-info WebSocket。
// 节点: ?type=1&secret=<node.secret>&version=&http=&tls=&socks=
// 管理员: ?type=0，JWT 优先从 Sec-WebSocket-Protocol 读取，兼容 secret query。
// 行为对应 Java WebSocketInterceptor + WebSocketServer。
func HandleSystemInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connType := q.Get("type")
	secret := q.Get("secret")

	if connType == "1" {
		// 节点连接：按 secret 匹配节点。
		// 用 Silent 日志级别查询——"查不到"是鉴权的正常结果，
		// 不应触发 GORM 的 record not found 警告刷屏。
		var node model.Node
		if err := model.DB.Session(&gorm.Session{Logger: logger.Discard}).
			Where("secret = ?", secret).First(&node).Error; err != nil {
			// 记录来源 IP 与脱敏 secret，便于定位残留/错配的节点 agent
			log.Printf("节点验证失败：未找到匹配的secret (来源IP: %s, secret: %s)",
				r.RemoteAddr, maskSecretForLog(secret))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		reportedBaseURL, reportErr := normalizeReportedPanelBaseURL(q.Get("panelUrl"))
		if reportErr != nil {
			// 地址可能属于内网，日志只记录节点 ID 和错误原因，不回显原始内容。
			log.Printf("节点 %d 上报的面板基址无效：%v", node.ID, reportErr)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(maxWebSocketMessageSize)
		handleNodeConn(conn, &node, q.Get("version"), q.Get("http"), q.Get("tls"), q.Get("socks"), reportedBaseURL)
		return
	}

	// 管理员连接：secret 为 JWT
	adminProtocol, protocolToken := adminJWTSubprotocol(r)
	if protocolToken != "" {
		secret = protocolToken
	} else {
		secret = adminTokenFromRequest(r, secret)
	}
	if !validateAdminToken(secret) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// 管理员 WS 额外 Origin 校验，缓解 CSWSH（跨站 WebSocket 劫持）
	if !adminOriginAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	responseHeader := http.Header{}
	if adminProtocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", adminProtocol)
	}
	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxWebSocketMessageSize)
	handleAdminConn(conn)
}

// normalizeReportedPanelBaseURL 规范化节点本机持久化的面板入口。
// 节点已经用该本机地址构造并完成当前 WebSocket 握手；反向代理可能改写 Host，因此不把外部入口强绑到 r.Host。
// 地址只会下发回同一已鉴权节点，面板自身不会请求它；公网 HTTP 是否允许仍会在每次升级时重新判断。
func normalizeReportedPanelBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxReportedPanelBaseURLLength {
		return "", fmt.Errorf("地址长度超过 %d 字节", maxReportedPanelBaseURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("只允许不含用户信息的 HTTP/HTTPS 基址")
	}
	if u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("基址不能包含查询参数或片段")
	}
	normalizedPath := strings.TrimRight(u.Path, "/")
	cleanedPath := path.Clean(normalizedPath)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if u.RawPath != "" || cleanedPath != normalizedPath {
		return "", fmt.Errorf("基址路径不能包含编码斜杠、重复分隔符或目录跳转")
	}
	u.Path = normalizedPath
	return strings.TrimRight(u.String(), "/"), nil
}

func adminJWTSubprotocol(r *http.Request) (string, string) {
	for _, protocol := range websocket.Subprotocols(r) {
		if strings.HasPrefix(protocol, "jwt.") {
			return protocol, strings.TrimPrefix(protocol, "jwt.")
		}
	}
	return "", ""
}

func adminTokenFromRequest(r *http.Request, fallback string) string {
	if _, token := adminJWTSubprotocol(r); token != "" {
		return token
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if auth != "" {
		return auth
	}
	return fallback
}

func validateAdminToken(token string) bool {
	claims, err := crypto.ParseToken(token)
	if err != nil {
		return false
	}
	user, err := middleware.LoadCurrentUser(claims.UserID())
	if err != nil || middleware.ValidateCurrentUser(user, claims) != nil {
		return false
	}
	return user.RoleID == 0 && user.MustChangePwd != 1
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// handleNodeConn 节点会话主循环
func handleNodeConn(conn *websocket.Conn, node *model.Node, version, httpP, tlsP, socksP, reportedBaseURL string) {
	s := NewSession(conn, SessionOptions{NodeID: node.ID, Secret: node.Secret, IsNode: true})
	// 连接整个生命周期固定使用建立时的 Hub，避免测试切换全局实例或未来热切换时把清理操作发到另一实例。
	hub := Default

	// 先持久化当前连接的版本、协议和历史入口，再把会话注册为命令接收者。
	// 这样管理员并发点击升级时，不会把上一会话遗留的地址下发给刚连上的旧 Agent。
	updates := map[string]interface{}{}
	if version != "" {
		updates["version"] = version
	}
	if httpP != "" {
		updates["http"] = atoiOr(httpP, node.HTTP)
	}
	if tlsP != "" {
		updates["tls"] = atoiOr(tlsP, node.TLS)
	}
	if socksP != "" {
		updates["socks"] = atoiOr(socksP, node.Socks)
	}
	if reportedBaseURL != "" {
		// 仅在 secret 校验及 WebSocket 升级均成功后覆盖历史地址。
		updates["last_connected_base_url"] = reportedBaseURL
		updates["last_connected_base_time"] = time.Now().UnixMilli()
	} else {
		// 当前连接无法证明旧历史地址仍属于它；清空可阻断泄露 secret 后污染地址、再诱导旧 Agent 升级的链路。
		updates["last_connected_base_url"] = ""
		updates["last_connected_base_time"] = int64(0)
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
		log.Printf("节点 %d 连接状态保存失败：%v", node.ID, err)
		s.Close()
		return
	}

	// 覆盖旧连接（与 Java 行为一致：新连接覆盖旧连接并主动关闭旧的）。
	if old := hub.AddNode(node.ID, s); old != nil {
		log.Printf("节点 %d 已有连接存在，新连接覆盖旧连接", node.ID)
		old.Close()
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("status", 1).Error; err != nil {
		log.Printf("节点 %d 在线状态保存失败：%v", node.ID, err)
		hub.RemoveNodeIfCurrent(node.ID, s)
		s.Close()
		return
	}
	log.Printf("节点 %d 连接建立成功，版本: %s", node.ID, version)

	// 广播节点上线
	broadcastStatus(hub, node.ID, 1)
	runNodeConnectedHook(node.ID)

	// 设置 pong 处理器
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	defer func() {
		if hub.RemoveNodeIfCurrent(node.ID, s) {
			model.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("status", 0)
			broadcastStatus(hub, node.ID, 0)
			log.Printf("节点 %d 离线", node.ID)
		}
		// 关闭会话 context，让该节点的在途请求立即失败、ping 循环退出。
		s.Close()
		hub.failPendingForNode(node.ID, "节点连接已断开")
	}()

	// 启动 ping 发送 goroutine（随会话 context 退出）
	pingTicker := time.NewTicker(pingPeriod)
	safeGo("node-ping", func() {
		runPingLoop(s, pingTicker)
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("节点 %d WebSocket 异常断开: %v", node.ID, err)
			}
			return
		}
		if len(raw) == 0 {
			continue
		}
		// 解密（如有）
		plain := crypto.DecryptIfNeeded(raw, node.Secret)
		text := string(plain)

		isHeartbeat := strings.Contains(text, "memory_usage")
		if isHeartbeat {
			// 心跳：回 {"type":"call"} 确认
			_ = s.WriteText([]byte(`{"type":"call"}`))
		} else if strings.Contains(text, "requestId") {
			// 命令响应：唤醒等待中的 SendMsg
			var resp commandResponse
			if err := json.Unmarshal(plain, &resp); err == nil && resp.RequestId != "" {
				if resp.Type == "Iperf3Progress" {
					if broadcast, err := buildIperf3ProgressBroadcast(node.ID, resp); err == nil {
						hub.Broadcast(broadcast)
					}
					continue
				}
				msg := resp.Message
				if msg == "" {
					msg = "无响应消息"
				}
				hub.resolvePending(s, resp.RequestId, GostResult{Msg: msg, Data: resp.Data})
			}
		}

		// 仅心跳消息转发给管理员会话（命令响应不是系统信息，
		// 前端会把 info 消息当 systemInfo 解析导致显示闪烁）
		if isHeartbeat {
			broadcast, err := json.Marshal(map[string]interface{}{
				"id":   strconv.FormatInt(node.ID, 10),
				"type": "info",
				"data": text,
			})
			if err == nil {
				hub.Broadcast(broadcast)
			}
		}
	}
}

func buildIperf3ProgressBroadcast(nodeID int64, resp commandResponse) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"id":   strconv.FormatInt(nodeID, 10),
		"type": "speed-test-progress",
		"data": resp.Data,
	})
}

// handleAdminConn 管理员会话主循环（仅接收广播，无主动消息处理）
func handleAdminConn(conn *websocket.Conn) {
	s := NewSession(conn, SessionOptions{})
	hub := Default
	hub.AddAdmin(s)

	// 设置 pong 处理器
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	defer func() {
		hub.RemoveAdmin(s)
		s.Close()
	}()

	// 启动 ping 发送 goroutine（随会话 context 退出）
	pingTicker := time.NewTicker(pingPeriod)
	safeGo("admin-ping", func() {
		runPingLoop(s, pingTicker)
	})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("管理员 WebSocket 异常断开: %v", err)
			}
			return
		}
	}
}

// runPingLoop 周期发送 ping，直到写失败或会话关闭。
// 用 context select 而不是裸 range ticker：会话关闭后立即退出，不泄漏。
func runPingLoop(s *Session, ticker *time.Ticker) {
	defer ticker.Stop()
	for {
		select {
		case <-s.Done():
			return
		case <-ticker.C:
			if err := s.writePing(); err != nil {
				s.Close()
				return
			}
		}
	}
}

// broadcastStatus 广播节点上下线状态 {id, type:"status", data:0|1}
func broadcastStatus(hub *Hub, nodeID int64, status int) {
	msg, err := json.Marshal(map[string]interface{}{
		"id":   strconv.FormatInt(nodeID, 10),
		"type": "status",
		"data": status,
	})
	if err == nil {
		hub.Broadcast(msg)
	}
}
