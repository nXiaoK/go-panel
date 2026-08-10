package ws

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/nXiaoK/go-panel/internal/crypto"
)

// GostResult 节点命令响应（对应 Java GostDto）
type GostResult struct {
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
	// OutcomeUnknown means a write was attempted but no definitive agent
	// response was received. It is internal transport state and is never JSON.
	OutcomeUnknown bool `json:"-"`
}

// SessionOptions configures a new connection-scoped session.
type SessionOptions struct {
	NodeID int64
	Secret string
	IsNode bool
}

// Session 单个 WebSocket 会话，writeMu 保证消息串行发送。
// 每个会话持有自己的 context：Close 取消它，所有该连接派生的
// goroutine（ping 循环等）通过 Done() 感知并退出，杜绝泄漏。
type Session struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	closeOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	// 节点会话属性（管理员会话 nodeID=0、secret 为空）
	NodeID int64
	Secret string
	IsNode bool
}

// NewSession 创建带生命周期 context 的会话。
func NewSession(conn *websocket.Conn, options SessionOptions) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{conn: conn, ctx: ctx, cancel: cancel, NodeID: options.NodeID, Secret: options.Secret, IsNode: options.IsNode}
}

// Done 在会话关闭后被关闭，连接派生的 goroutine 以此退出。
func (s *Session) Done() <-chan struct{} {
	if s.ctx == nil {
		// 零值会话（仅测试或历史构造）没有 context；返回永不关闭的通道。
		return nil
	}
	return s.ctx.Done()
}

// WriteText 串行发送文本消息；节点会话自动加密
func (s *Session) WriteText(message []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	payload := message
	if s.IsNode && s.Secret != "" {
		payload = crypto.EncryptIfPossible(message, s.Secret, time.Now().UnixMilli())
	}
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteMessage(websocket.TextMessage, payload)
}

// writePing 发送 ping 消息
func (s *Session) writePing() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteMessage(websocket.PingMessage, nil)
}

// Close 幂等关闭：取消会话 context 并关闭底层连接。
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.conn.Close()
	})
}

// Hub 会话管理中心（对应 Java WebSocketServer 的静态状态）
type Hub struct {
	mu      sync.RWMutex
	nodes   map[int64]*Session    // nodeID -> 节点会话
	admins  map[*Session]struct{} // 管理员（前端）会话
	pending sync.Map              // requestId -> *pendingRequest
}

type pendingRequest struct {
	nodeID  int64
	session *Session
	result  chan GostResult
	// sent is published while holding Hub.mu and read by lifecycle cancellation
	// while holding the exclusive lock.
	sent bool
}

// NewHub 创建 Hub
func NewHub() *Hub {
	return &Hub{
		nodes:  make(map[int64]*Session),
		admins: make(map[*Session]struct{}),
	}
}

// 全局 Hub 实例（业务层直接调用 ws.SendMsg）
var Default = NewHub()

// AddAdmin 注册管理员会话
func (h *Hub) AddAdmin(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.admins[s] = struct{}{}
}

// RemoveAdmin 移除管理员会话
func (h *Hub) RemoveAdmin(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.admins, s)
}

// AddNode 注册节点会话，返回被覆盖的旧会话（如有）
func (h *Hub) AddNode(nodeID int64, s *Session) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.nodes[nodeID]
	h.nodes[nodeID] = s
	if old != nil && old != s {
		h.cancelPendingForSession(nodeID, old, "节点连接已替换")
	}
	return old
}

// RemoveNodeIfCurrent 仅当 s 仍是该节点的活跃会话时移除，返回是否移除。
// 用于覆盖式重连场景：旧连接关闭时不应误下线新连接。
func (h *Hub) RemoveNodeIfCurrent(nodeID int64, s *Session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancelPendingForSession(nodeID, s, "节点连接已断开")
	if cur, ok := h.nodes[nodeID]; ok && cur == s {
		delete(h.nodes, nodeID)
		return true
	}
	return false
}

// GetNode 获取节点会话
func (h *Hub) GetNode(nodeID int64) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nodes[nodeID]
}

// CloseAll 停机时关闭全部会话：在锁内摘除会话并失败所有在途请求，
// 在锁外关闭连接，避免 close 回调反过来抢锁死锁。
func (h *Hub) CloseAll(reason string) {
	h.mu.Lock()
	sessions := make([]*Session, 0, len(h.nodes)+len(h.admins))
	for nodeID, s := range h.nodes {
		sessions = append(sessions, s)
		delete(h.nodes, nodeID)
	}
	for s := range h.admins {
		sessions = append(sessions, s)
		delete(h.admins, s)
	}
	h.pending.Range(func(key, value interface{}) bool {
		pending, ok := value.(*pendingRequest)
		if ok && h.pending.CompareAndDelete(key, pending) {
			pending.deliver(GostResult{Msg: reason, OutcomeUnknown: pending.sent})
		}
		return true
	})
	h.mu.Unlock()

	for _, s := range sessions {
		s.Close()
	}
}

// Broadcast 向所有管理员会话广播消息
func (h *Hub) Broadcast(message []byte) {
	h.mu.RLock()
	sessions := make([]*Session, 0, len(h.admins))
	for s := range h.admins {
		sessions = append(sessions, s)
	}
	h.mu.RUnlock()
	for _, s := range sessions {
		if err := s.WriteText(message); err != nil {
			log.Printf("管理员 WebSocket 广播失败，移除会话: %v", err)
			h.RemoveAdmin(s)
			s.Close()
		}
	}
}

// resolvePending 收到带 requestId 的响应时，仅唤醒绑定到响应来源会话的等待者。
func (h *Hub) resolvePending(session *Session, requestId string, res GostResult) {
	v, ok := h.pending.Load(requestId)
	if !ok {
		return
	}
	pending, ok := v.(*pendingRequest)
	if !ok || pending.session != session || !h.pending.CompareAndDelete(requestId, pending) {
		return
	}
	pending.deliver(res)
}

func (h *Hub) cancelPendingForSession(nodeID int64, session *Session, message string) {
	if session == nil {
		return
	}
	h.pending.Range(func(key, value interface{}) bool {
		pending, ok := value.(*pendingRequest)
		if ok && pending.nodeID == nodeID && pending.session == session && h.pending.CompareAndDelete(key, pending) {
			pending.deliver(GostResult{Msg: message, OutcomeUnknown: pending.sent})
		}
		return true
	})
}

// failPendingForNode 立即失败该节点全部在途请求（不区分会话）。
// 用于连接关闭/停机：等待方立刻拿到结果而不是耗尽超时。
// 持有 h.mu 保护 pending.sent 的读取（与 sendMsg 的 RLock 写互斥）。
func (h *Hub) failPendingForNode(nodeID int64, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending.Range(func(key, value interface{}) bool {
		pending, ok := value.(*pendingRequest)
		if ok && pending.nodeID == nodeID && h.pending.CompareAndDelete(key, pending) {
			pending.deliver(GostResult{Msg: message, OutcomeUnknown: pending.sent})
		}
		return true
	})
}

func (p *pendingRequest) deliver(res GostResult) {
	select {
	case p.result <- res:
	default:
	}
}

// sendTimeout 等待节点响应的超时时间（与 Java future.get(10s) 一致）
const sendTimeout = 10 * time.Second

// SendMsg 向节点发送命令并等待响应（对应 Java WebSocketServer.send_msg）。
// 消息格式 {type, data, requestId}，节点回包带 requestId。
func (h *Hub) SendMsg(nodeID int64, data interface{}, msgType string) GostResult {
	return h.SendMsgWithTimeout(nodeID, data, msgType, sendTimeout)
}

// SendMsgWithTimeout 向节点发送命令并按指定超时等待响应。
func (h *Hub) SendMsgWithTimeout(nodeID int64, data interface{}, msgType string, timeout time.Duration) GostResult {
	if timeout <= 0 {
		timeout = sendTimeout
	}
	return h.sendMsg(nodeID, data, msgType, &timeout)
}

// SendMsgLifecycle 向节点发送命令并等待到响应或目标节点会话结束。
// 它没有总响应 deadline；节点断连或被新会话替换时会立即返回失败。
func (h *Hub) SendMsgLifecycle(nodeID int64, data interface{}, msgType string) GostResult {
	return h.sendMsg(nodeID, data, msgType, nil)
}

func (h *Hub) sendMsg(nodeID int64, data interface{}, msgType string, timeout *time.Duration) GostResult {
	requestId := uuid.NewString()
	payload, err := json.Marshal(map[string]interface{}{
		"type":      msgType,
		"data":      data,
		"requestId": requestId,
	})
	if err != nil {
		return GostResult{Msg: "发送消息失败: " + err.Error()}
	}

	pending := &pendingRequest{
		nodeID: nodeID,
		result: make(chan GostResult, 1),
	}
	h.mu.RLock()
	s := h.nodes[nodeID]
	if s == nil {
		h.mu.RUnlock()
		log.Printf("发送消息失败：节点 %d 不在线", nodeID)
		return GostResult{Msg: "节点不在线"}
	}
	pending.session = s
	if _, loaded := h.pending.LoadOrStore(requestId, pending); loaded {
		h.mu.RUnlock()
		return GostResult{Msg: "发送消息失败: requestId 冲突"}
	}
	defer h.pending.CompareAndDelete(requestId, pending)

	writeErr := s.WriteText(payload)
	if writeErr == nil {
		pending.sent = true
	}
	h.mu.RUnlock()
	if writeErr != nil {
		return GostResult{Msg: "发送消息失败: " + writeErr.Error(), OutcomeUnknown: true}
	}

	if timeout == nil {
		return <-pending.result
	}
	timer := time.NewTimer(*timeout)
	defer timer.Stop()
	select {
	case res := <-pending.result:
		return res
	case <-timer.C:
		log.Printf("节点 %d 响应超时", nodeID)
		return GostResult{Msg: "等待响应超时", OutcomeUnknown: true}
	}
}

// SendMsg 包级快捷方法
func SendMsg(nodeID int64, data interface{}, msgType string) GostResult {
	return Default.SendMsg(nodeID, data, msgType)
}

// SendMsgWithTimeout 包级快捷方法
func SendMsgWithTimeout(nodeID int64, data interface{}, msgType string, timeout time.Duration) GostResult {
	return Default.SendMsgWithTimeout(nodeID, data, msgType, timeout)
}

// SendMsgLifecycle 包级快捷方法。
func SendMsgLifecycle(nodeID int64, data interface{}, msgType string) GostResult {
	return Default.SendMsgLifecycle(nodeID, data, msgType)
}
