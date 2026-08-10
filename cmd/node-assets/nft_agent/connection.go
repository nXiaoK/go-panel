package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nXiaoK/go-panel/cmd/node-assets/internal/nodecrypto"
)

func safeWriteMessage(conn *websocket.Conn, msgType int, data []byte) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	return conn.WriteMessage(msgType, data)
}

// nftExecTimeout 单条 nft 命令的最大执行时间。避免 nftables 锁死时 agent 永久阻塞。
func main() {
	// 重连退避：1s 起指数翻倍、±20% 抖动、30s 封顶；连接成功后重置，
	// 保证长时间在线后的偶发断连从 1 秒快速重连，而不是直接落到上限。
	backoff := NewBackoff(nil)
	for {
		cfg, err := loadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			time.Sleep(backoff.Next())
			continue
		}
		if err := run(cfg, backoff.Reset); err != nil {
			fmt.Fprintf(os.Stderr, "agent disconnected: %v\n", err)
		}
		time.Sleep(backoff.Next())
	}
}

func run(cfg config, onConnected func()) error {
	panelBaseURL, err := normalizePanelBaseURL(cfg.ServerAddr)
	if err != nil {
		return err
	}
	wsURL, err := buildWSURL(cfg)
	if err != nil {
		return err
	}
	// 自定义 Dialer：限制握手超时，避免 TCP 接受但 WS 握手卡住时永久阻塞
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Println("nftables agent connected")
	if onConnected != nil {
		onConnected()
	}

	// 命令经由双通道调度器执行：读循环只做解密/解析/入队，
	// 长时诊断（iperf/tcpping）不会阻塞心跳与 nft 变更命令。
	dispatcher := newCommandDispatcher(context.Background(), &liveCommandExecutor{
		conn: conn, secret: cfg.Secret, panelBaseURL: panelBaseURL,
	}, func(resp commandResponse) error {
		payload, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		return safeWriteMessage(conn, websocket.TextMessage, nodecrypto.EncryptIfPossible(payload, cfg.Secret))
	})
	defer dispatcher.Close()

	done := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			plain := nodecrypto.DecryptIfNeeded(raw, cfg.Secret)
			var cmd commandMessage
			if err := json.Unmarshal(plain, &cmd); err != nil {
				sendResponse(conn, cfg.Secret, commandResponse{
					Type:    "ParseErrorResponse",
					Success: false,
					Message: "parse command: " + err.Error(),
				})
				continue
			}
			_ = dispatcher.Dispatch(cmd)
		}
	}()

	heartbeatTicker := time.NewTicker(2 * time.Second)
	defer heartbeatTicker.Stop()
	flowTicker := time.NewTicker(30 * time.Second)
	defer flowTicker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-heartbeatTicker.C:
			if err := sendSystemInfo(conn, cfg.Secret); err != nil {
				return err
			}
		case <-flowTicker.C:
			// 上报器在独立 goroutine 内限时运行：卡死的上报进程不会
			// 阻塞心跳循环导致连接被判死。
			go runFlowReporterOnce(cfg.ServerAddr, cfg.Secret, runBoundedCommand)
		}
	}
}

// liveCommandExecutor 执行面板命令的生产实现。iperf 进度等旁路消息
// 仍直接经 conn 发送；最终响应由调度器统一回写。
type liveCommandExecutor struct {
	conn             *websocket.Conn
	secret           string
	panelBaseURL     string
	upgradeRunner    func(json.RawMessage, string) error
	restartScheduler func(string)
	upgradeLog       io.Writer
}

func (e *liveCommandExecutor) Execute(_ context.Context, cmd commandMessage) commandResponse {
	conn, secret := e.conn, e.secret
	resp := commandResponse{
		Type:      cmd.Type + "Response",
		Success:   true,
		Message:   "OK",
		RequestID: cmd.RequestID,
	}

	switch cmd.Type {
	case "ApplyNftRules":
		if err := applyNftRulesWithRunner(applyScriptPath, runBoundedCommand); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "ListNftRules":
		view, err := listNftRules()
		if err != nil {
			resp.Success = false
			resp.Message = err.Error()
		} else {
			resp.Data = view
		}
	case "AddNftRule":
		if err := addNftRule(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "AddNftRules":
		if err := addNftRules(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "DeleteNftRule":
		if err := deleteNftRule(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "DeleteNftRules":
		if err := deleteNftRules(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "ReplaceNftRules":
		if err := replaceNftRules(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "FindRuleHandles":
		view, err := findRuleHandles(cmd.Data)
		if err != nil {
			resp.Success = false
			resp.Message = err.Error()
		} else {
			resp.Data = view
		}
	case "FlushConntrack":
		if err := flushConntrack(cmd.Data); err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "SetProtocol":
	case "UpgradeNode":
		upgradeRunner := e.upgradeRunner
		if upgradeRunner == nil {
			upgradeRunner = upgradeNodeFromLocalBaseURL
		}
		if err := upgradeRunner(cmd.Data, e.panelBaseURL); err != nil {
			resp.Success = false
			resp.Message = err.Error()
			writeNodeUpgradeFailure(e.upgradeLog, cmd.RequestID, "install_assets", err)
		} else {
			if e.restartScheduler != nil {
				e.restartScheduler(cmd.RequestID)
			} else {
				go restartNodeServicesSoon(cmd.RequestID)
			}
		}
	case "TcpPing":
		result, err := tcpPing(cmd.Data, cmd.RequestID)
		resp.Type = "TcpPingResponse"
		resp.Data = result
		if err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "Iperf3Server":
		result, err := startIperf3Server(cmd.Data)
		resp.Type = "Iperf3ServerResponse"
		resp.Data = result
		if err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	case "Iperf3Client":
		result, err := runIperf3Client(conn, secret, cmd.Data, cmd.RequestID)
		resp.Type = "Iperf3ClientResponse"
		resp.Data = result
		if err != nil {
			resp.Success = false
			resp.Message = err.Error()
		}
	default:
		resp.Success = false
		resp.Message = "unknown command type: " + cmd.Type
	}

	return resp
}

func sendSystemInfo(conn *websocket.Conn, secret string) error {
	rx, tx := readNetworkBytes()
	payload, err := json.Marshal(map[string]any{
		"uptime":            readUptime(),
		"bytes_received":    rx,
		"bytes_transmitted": tx,
		"cpu_usage":         readCPUUsage(),
		"memory_usage":      readMemoryUsage(),
	})
	if err != nil {
		return err
	}
	return safeWriteMessage(conn, websocket.TextMessage, nodecrypto.EncryptIfPossible(payload, secret))
}

func sendResponse(conn *websocket.Conn, secret string, resp commandResponse) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = safeWriteMessage(conn, websocket.TextMessage, nodecrypto.EncryptIfPossible(payload, secret))
}

// normalizePanelBaseURL 将节点配置中的面板地址规范为 HTTP(S) 基址。
// 未写协议的历史配置继续按 HTTP 兼容；userinfo、query 和 fragment 会被拒绝，避免升级路径被歧义解析。
func normalizePanelBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", fmt.Errorf("missing SERVER_ADDR")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid SERVER_ADDR: only HTTP/HTTPS panel base URLs are supported")
	}
	if u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid SERVER_ADDR: query and fragment are not allowed")
	}
	normalizedPath := strings.TrimRight(u.Path, "/")
	cleanedPath := path.Clean(normalizedPath)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if u.RawPath != "" || cleanedPath != normalizedPath {
		return "", fmt.Errorf("invalid SERVER_ADDR: ambiguous or traversing paths are not allowed")
	}
	u.Path = normalizedPath
	return strings.TrimRight(u.String(), "/"), nil
}

func buildWSURL(cfg config) (string, error) {
	panelBaseURL, err := normalizePanelBaseURL(cfg.ServerAddr)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(panelBaseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/system-info"
	values := url.Values{}
	values.Set("type", "1")
	values.Set("secret", cfg.Secret)
	values.Set("version", version)
	values.Set("http", "0")
	values.Set("tls", "0")
	values.Set("socks", "0")
	// panelUrl 来自节点本机持久化配置；面板记录后用于后续升级，避免全局入口变更影响该节点。
	values.Set("panelUrl", panelBaseURL)
	u.RawQuery = values.Encode()
	return u.String(), nil
}

func loadConfig(path string) (config, error) {
	env := readEnvFile(path)
	cfg := config{
		ServerAddr: env["SERVER_ADDR"],
		Secret:     env["SECRET"],
	}
	if cfg.ServerAddr == "" || cfg.Secret == "" {
		return cfg, fmt.Errorf("%s missing SERVER_ADDR or SECRET", path)
	}
	return cfg, nil
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out
}

func readUptime() uint64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return uint64(f)
}

// readNetworkBytes 读取 /proc/net/dev，累计除回环外所有网卡的收发字节数
func readNetworkBytes() (rx, tx uint64) {
	raw, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// lastCPUTotal/lastCPUIdle 保存上次 CPU 采样的累计值。
// 用 atomic 存储，避免心跳 goroutine 与未来可能的并发读产生数据竞争。
var lastCPUTotal, lastCPUIdle atomic.Uint64

// readCPUUsage 读取 /proc/stat 计算与上次采样之间的 CPU 使用率
func readCPUUsage() float64 {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	prevTotal := lastCPUTotal.Swap(total)
	prevIdle := lastCPUIdle.Swap(idle)
	if prevTotal == 0 || total <= prevTotal {
		return 0
	}
	totalDiff := total - prevTotal
	idleDiff := idle - prevIdle
	return float64(totalDiff-idleDiff) / float64(totalDiff) * 100
}

func readMemoryUsage() float64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, available float64
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total, _ = strconv.ParseFloat(fields[1], 64)
		case "MemAvailable":
			available, _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	if total <= 0 {
		return 0
	}
	return (total - available) / total * 100
}
