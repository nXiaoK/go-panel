package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSendMsgLifecycleReturnsNodeResponseWithoutDeadline(t *testing.T) {
	h := NewHub()
	session, peer := newTestSession(t, 11)
	h.AddNode(11, session)

	result := make(chan GostResult, 1)
	go func() {
		result <- h.SendMsgLifecycle(11, map[string]string{"rule": "accept"}, "ApplyNftRules")
	}()
	requestID := readTestRequestID(t, peer)
	h.resolvePending(session, requestID, GostResult{Msg: "success", Data: json.RawMessage(`{"ok":true}`)})
	got := <-result
	if got.Msg != "success" || string(got.Data) != `{"ok":true}` {
		t.Fatalf("SendMsgLifecycle()=%+v", got)
	}
	if got.OutcomeUnknown {
		t.Fatal("normal node response was marked outcome-unknown")
	}
}

func TestSendMsgLifecycleFailsWhenCurrentSessionDisconnects(t *testing.T) {
	h := NewHub()
	session, peer := newTestSession(t, 12)
	h.AddNode(12, session)

	result := make(chan GostResult, 1)
	go func() {
		result <- h.SendMsgLifecycle(12, nil, "ApplyNftRules")
	}()
	_ = readTestRequestID(t, peer)

	if !h.RemoveNodeIfCurrent(12, session) {
		t.Fatal("current node session was not removed")
	}
	select {
	case got := <-result:
		if got.Msg != "节点连接已断开" {
			t.Fatalf("disconnect result=%+v", got)
		}
		if !got.OutcomeUnknown {
			t.Fatal("disconnect after write was not marked outcome-unknown")
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle wait was not released by disconnect")
	}
}

func TestSendMsgLifecycleReplacementOnlyFailsOldSessionRequests(t *testing.T) {
	h := NewHub()
	oldSession, oldPeer := newTestSession(t, 13)
	h.AddNode(13, oldSession)

	oldResult := make(chan GostResult, 1)
	go func() {
		oldResult <- h.SendMsgLifecycle(13, nil, "ApplyNftRules")
	}()
	_ = readTestRequestID(t, oldPeer)

	newSession, newPeer := newTestSession(t, 13)
	if replaced := h.AddNode(13, newSession); replaced != oldSession {
		t.Fatalf("replaced session=%p, want %p", replaced, oldSession)
	}
	select {
	case got := <-oldResult:
		if got.Msg != "节点连接已替换" {
			t.Fatalf("replacement result=%+v", got)
		}
		if !got.OutcomeUnknown {
			t.Fatal("replacement after write was not marked outcome-unknown")
		}
	case <-time.After(time.Second):
		t.Fatal("old lifecycle wait was not released by replacement")
	}

	newResult := make(chan GostResult, 1)
	go func() {
		newResult <- h.SendMsgLifecycle(13, nil, "ApplyNftRules")
	}()
	newRequestID := readTestRequestID(t, newPeer)

	// The old connection's deferred cleanup must not remove or cancel work
	// registered against the replacement session.
	if h.RemoveNodeIfCurrent(13, oldSession) {
		t.Fatal("old session removed the replacement")
	}
	h.resolvePending(oldSession, newRequestID, GostResult{Msg: "spoofed-old-response"})
	select {
	case got := <-newResult:
		t.Fatalf("old session resolved replacement request: %+v", got)
	case <-time.After(30 * time.Millisecond):
	}
	h.resolvePending(newSession, newRequestID, GostResult{Msg: "success"})
	select {
	case got := <-newResult:
		if got.Msg != "success" {
			t.Fatalf("new session response=%+v", got)
		}
		if got.OutcomeUnknown {
			t.Fatal("normal replacement-session response was marked outcome-unknown")
		}
	case <-time.After(time.Second):
		t.Fatal("new session request did not complete")
	}
}

func TestSendMsgLifecycleRegistrationAndReplacementRaceDoesNotHang(t *testing.T) {
	for i := 0; i < 50; i++ {
		h := NewHub()
		oldSession, oldPeer := newTestSession(t, 14)
		newSession, newPeer := newTestSession(t, 14)
		h.AddNode(14, oldSession)

		result := make(chan GostResult, 1)
		go func() {
			result <- h.SendMsgLifecycle(14, nil, "ApplyNftRules")
		}()
		replaced := make(chan struct{})
		go func() {
			h.AddNode(14, newSession)
			close(replaced)
		}()

		// Depending on which operation linearizes first, the command is sent
		// to the old session and cancelled, or to the new session and answered.
		requestIDs := make(chan struct {
			session *Session
			id      string
		}, 2)
		readMaybe := func(session *Session, peer *websocket.Conn) {
			_ = peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, raw, err := peer.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(raw, &request) == nil && request.RequestID != "" {
				requestIDs <- struct {
					session *Session
					id      string
				}{session: session, id: request.RequestID}
			}
		}
		go readMaybe(oldSession, oldPeer)
		go readMaybe(newSession, newPeer)
		<-replaced

		select {
		case request := <-requestIDs:
			h.resolvePending(request.session, request.id, GostResult{Msg: "success"})
		case got := <-result:
			if got.Msg != "节点连接已替换" && !strings.HasPrefix(got.Msg, "发送消息失败:") {
				t.Fatalf("iteration %d race result=%+v", i, got)
			}
			if !got.OutcomeUnknown {
				t.Fatalf("iteration %d post-write race was not outcome-unknown: %+v", i, got)
			}
			continue
		case <-time.After(time.Second):
			t.Fatalf("iteration %d command was neither sent nor cancelled", i)
		}

		select {
		case got := <-result:
			if got.Msg != "success" && got.Msg != "节点连接已替换" {
				t.Fatalf("iteration %d completion=%+v", i, got)
			}
			if got.Msg == "节点连接已替换" && !got.OutcomeUnknown {
				t.Fatalf("iteration %d replacement was not outcome-unknown: %+v", i, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d lifecycle wait hung across replacement", i)
		}
	}
}

func TestSendMsgLifecycleReplacementWaitsForCurrentSessionWrite(t *testing.T) {
	h := NewHub()
	oldSession, oldPeer := newTestSession(t, 15)
	newSession, _ := newTestSession(t, 15)
	h.AddNode(15, oldSession)

	// Stall the websocket write after SendMsgLifecycle has selected and
	// registered against the current session.
	oldSession.writeMu.Lock()
	result := make(chan GostResult, 1)
	go func() {
		result <- h.SendMsgLifecycle(15, nil, "ApplyNftRules")
	}()
	waitForPendingCount(t, h, 1)

	replaced := make(chan *Session, 1)
	go func() {
		replaced <- h.AddNode(15, newSession)
	}()
	select {
	case <-replaced:
		oldSession.writeMu.Unlock()
		t.Fatal("session replacement linearized before current-session write")
	case <-time.After(30 * time.Millisecond):
	}

	oldSession.writeMu.Unlock()
	_ = readTestRequestID(t, oldPeer)
	select {
	case got := <-replaced:
		if got != oldSession {
			t.Fatalf("replaced session=%p, want %p", got, oldSession)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not complete after write")
	}
	select {
	case got := <-result:
		if got.Msg != "节点连接已替换" || !got.OutcomeUnknown {
			t.Fatalf("post-write replacement result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not release lifecycle wait")
	}
}

func TestSendMsgLifecycleWriteFailureIsOutcomeUnknown(t *testing.T) {
	h := NewHub()
	session, _ := newTestSession(t, 16)
	h.AddNode(16, session)
	session.Close()

	got := h.SendMsgLifecycle(16, nil, "ApplyNftRules")
	if !strings.HasPrefix(got.Msg, "发送消息失败:") {
		t.Fatalf("write failure result=%+v", got)
	}
	if !got.OutcomeUnknown {
		t.Fatalf("write failure was not marked outcome-unknown: %+v", got)
	}
}

func TestSendMsgLifecyclePreWriteFailuresHaveKnownOutcome(t *testing.T) {
	h := NewHub()
	if got := h.SendMsgLifecycle(17, nil, "ApplyNftRules"); got.Msg != "节点不在线" || got.OutcomeUnknown {
		t.Fatalf("offline result=%+v", got)
	}

	session, _ := newTestSession(t, 17)
	h.AddNode(17, session)
	if got := h.SendMsgLifecycle(17, make(chan int), "ApplyNftRules"); !strings.HasPrefix(got.Msg, "发送消息失败:") || got.OutcomeUnknown {
		t.Fatalf("marshal failure result=%+v", got)
	}
}

func TestGostResultOutcomeUnknownIsInternalTransportState(t *testing.T) {
	payload, err := json.Marshal(GostResult{Msg: "节点连接已断开", OutcomeUnknown: true})
	if err != nil {
		t.Fatalf("marshal GostResult: %v", err)
	}
	if strings.Contains(string(payload), "OutcomeUnknown") || strings.Contains(string(payload), "outcome") {
		t.Fatalf("internal outcome state leaked into JSON: %s", payload)
	}
}

func TestSendMsgWithTimeoutMarksPostWriteTimeoutOutcomeUnknown(t *testing.T) {
	h := NewHub()
	session, peer := newTestSession(t, 18)
	h.AddNode(18, session)

	got := h.SendMsgWithTimeout(18, nil, "ApplyNftRules", 10*time.Millisecond)
	_ = readTestRequestID(t, peer)
	if got.Msg != "等待响应超时" || !got.OutcomeUnknown {
		t.Fatalf("post-write timeout result=%+v", got)
	}
}

func waitForPendingCount(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		count := 0
		h.pending.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		if count == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending count did not reach %d", want)
}

func newTestSession(t *testing.T, nodeID int64) (*Session, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
	}))
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	var conn *websocket.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		peer.Close()
		server.Close()
		t.Fatal("test websocket was not accepted")
	}
	server.Close()
	session := NewSession(conn, SessionOptions{NodeID: nodeID, IsNode: true})
	t.Cleanup(func() {
		session.Close()
		peer.Close()
	})
	return session, peer
}

func readTestRequestID(t *testing.T, peer *websocket.Conn) string {
	t.Helper()
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	_, raw, err := peer.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket request: %v", err)
	}
	var request struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode websocket request: %v", err)
	}
	if request.RequestID == "" {
		t.Fatal("websocket request has no requestId")
	}
	return request.RequestID
}
