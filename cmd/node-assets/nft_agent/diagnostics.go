package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type tcpPingResponse struct {
	IP           string  `json:"ip"`
	Port         int     `json:"port"`
	Success      bool    `json:"success"`
	AverageTime  float64 `json:"averageTime"`
	PacketLoss   float64 `json:"packetLoss"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
	RequestID    string  `json:"requestId,omitempty"`
}

type iperf3Summary struct {
	SentMbps      float64 `json:"sentMbps"`
	ReceivedMbps  float64 `json:"receivedMbps"`
	SentBytes     int64   `json:"sentBytes"`
	ReceivedBytes int64   `json:"receivedBytes"`
	Seconds       float64 `json:"seconds"`
	Retransmits   int64   `json:"retransmits"`
	LatencyMs     float64 `json:"latencyMs,omitempty"`
	LossPercent   float64 `json:"lossPercent,omitempty"`
	PingSamples   int     `json:"pingSamples,omitempty"`
}

type iperf3Interval struct {
	TestID        string  `json:"testId,omitempty"`
	Stream        string  `json:"stream"`
	StartSeconds  float64 `json:"startSeconds"`
	EndSeconds    float64 `json:"endSeconds"`
	Mbps          float64 `json:"mbps"`
	TransferBytes int64   `json:"transferBytes"`
	Retransmits   int64   `json:"retransmits"`
	LatencyMs     float64 `json:"latencyMs,omitempty"`
	LossPercent   float64 `json:"lossPercent,omitempty"`
	PingSamples   int     `json:"pingSamples,omitempty"`
	RawLine       string  `json:"rawLine,omitempty"`
}

type pingMetrics struct {
	LatencyMs   float64 `json:"latencyMs"`
	LossPercent float64 `json:"lossPercent"`
	Samples     int     `json:"samples"`
}

type pingSampler struct {
	mu            sync.Mutex
	samples       int
	lost          int
	successes     int
	totalLatency  float64
	latestLatency float64
}

func flushConntrack(raw json.RawMessage) error {
	if _, err := exec.LookPath("conntrack"); err != nil {
		return fmt.Errorf("conntrack 未安装: %w", err)
	}
	return flushConntrackWithRunner(raw, runBoundedCommand)
}

func flushConntrackWithRunner(raw json.RawMessage, run commandRunner) error {
	var req struct {
		Port      int      `json:"port"`
		Protocol  string   `json:"protocol"`
		Protocols []string `json:"protocols"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("parse request: %v", err)
	}
	if req.Port <= 0 || req.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	protocols, err := normalizeConntrackProtocols(req.Protocol, req.Protocols)
	if err != nil {
		return err
	}
	for _, protocol := range protocols {
		ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
		output, err := run(ctx, "conntrack", "-D", "-p", protocol, "--dport", strconv.Itoa(req.Port))
		cancel()
		if err != nil && !conntrackDeleteNoEntries(output) {
			return fmt.Errorf("flush conntrack %s/%d failed: %v, output: %s", protocol, req.Port, err, string(output))
		}
	}
	return nil
}

func normalizeConntrackProtocols(protocol string, protocols []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	add := func(value string) error {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "all", "both", "tcp_udp", "tcp+udp", "tcp,udp":
			if !seen["tcp"] {
				out = append(out, "tcp")
				seen["tcp"] = true
			}
			if !seen["udp"] {
				out = append(out, "udp")
				seen["udp"] = true
			}
		case "tcp", "udp":
			normalized := strings.ToLower(strings.TrimSpace(value))
			if !seen[normalized] {
				out = append(out, normalized)
				seen[normalized] = true
			}
		default:
			return fmt.Errorf("unsupported protocol: %s", value)
		}
		return nil
	}
	for _, value := range protocols {
		if err := add(value); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		if err := add(protocol); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func conntrackDeleteNoEntries(output []byte) bool {
	lower := strings.ToLower(string(output))
	return strings.Contains(lower, "0 flow entries") || strings.Contains(lower, "0 entries")
}

func tcpPing(raw json.RawMessage, requestID string) (tcpPingResponse, error) {
	var req struct {
		IP      string `json:"ip"`
		Port    int    `json:"port"`
		Count   int    `json:"count"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return tcpPingResponse{}, err
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Timeout <= 0 {
		req.Timeout = 1000
	}

	address := net.JoinHostPort(req.IP, strconv.Itoa(req.Port))
	timeout := time.Duration(req.Timeout) * time.Millisecond
	var total time.Duration
	failures := 0
	var lastErr error
	for i := 0; i < req.Count; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", address, timeout)
		if err != nil {
			failures++
			lastErr = err
			continue
		}
		total += time.Since(start)
		_ = conn.Close()
	}

	successes := req.Count - failures
	resp := tcpPingResponse{
		IP:          req.IP,
		Port:        req.Port,
		Success:     successes > 0,
		PacketLoss:  float64(failures) / float64(req.Count) * 100,
		AverageTime: 0,
		RequestID:   requestID,
	}
	if successes > 0 {
		resp.AverageTime = float64(total.Milliseconds()) / float64(successes)
	}
	if lastErr != nil {
		resp.ErrorMessage = lastErr.Error()
	}
	return resp, nil
}

var iperf3IntervalPattern = regexp.MustCompile(`^\[\s*([^\]]+)\]\s+([0-9.]+)-\s*([0-9.]+)\s+sec\s+([0-9.]+)\s+([KMGT]?Bytes)\s+([0-9.]+)\s+([KMGT]?bits/sec)(?:\s+([0-9]+))?`)
var pingLossPattern = regexp.MustCompile(`([0-9.]+)%\s+packet loss`)
var pingAvgPattern = regexp.MustCompile(`(?:rtt|round-trip)[^=]*=\s*[0-9.]+/([0-9.]+)/[0-9.]+`)

func roundIperfMbps(value float64) float64 {
	return math.Round(value*100) / 100
}

func roundMetric(value float64) float64 {
	return math.Round(value*100) / 100
}

func parseFloatOrZero(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func parseInt64OrZero(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func bytesMultiplier(unit string) float64 {
	switch strings.ToUpper(strings.TrimSuffix(unit, "Bytes")) {
	case "K":
		return 1024
	case "M":
		return 1024 * 1024
	case "G":
		return 1024 * 1024 * 1024
	case "T":
		return 1024 * 1024 * 1024 * 1024
	default:
		return 1
	}
}

func bitsMultiplier(unit string) float64 {
	switch strings.ToUpper(strings.TrimSuffix(unit, "bits/sec")) {
	case "K":
		return 1_000
	case "M":
		return 1_000_000
	case "G":
		return 1_000_000_000
	case "T":
		return 1_000_000_000_000
	default:
		return 1
	}
}

func parseIperf3IntervalLine(line string) (iperf3Interval, bool) {
	match := iperf3IntervalPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) == 0 {
		return iperf3Interval{}, false
	}
	transfer := parseFloatOrZero(match[4]) * bytesMultiplier(match[5])
	bitsPerSecond := parseFloatOrZero(match[6]) * bitsMultiplier(match[7])
	return iperf3Interval{
		Stream:        strings.TrimSpace(match[1]),
		StartSeconds:  parseFloatOrZero(match[2]),
		EndSeconds:    parseFloatOrZero(match[3]),
		TransferBytes: int64(math.Round(transfer)),
		Mbps:          roundIperfMbps(bitsPerSecond / 1_000_000),
		Retransmits:   parseInt64OrZero(match[8]),
		RawLine:       strings.TrimSpace(line),
	}, true
}

func parsePingMetrics(output string) (pingMetrics, bool) {
	lossMatch := pingLossPattern.FindStringSubmatch(output)
	avgMatch := pingAvgPattern.FindStringSubmatch(output)
	if len(lossMatch) == 0 && len(avgMatch) == 0 {
		return pingMetrics{}, false
	}
	metrics := pingMetrics{Samples: 1}
	if len(lossMatch) > 1 {
		metrics.LossPercent = roundMetric(parseFloatOrZero(lossMatch[1]))
	}
	if len(avgMatch) > 1 {
		metrics.LatencyMs = roundMetric(parseFloatOrZero(avgMatch[1]))
	}
	return metrics, true
}

func (s *pingSampler) record(metrics pingMetrics, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	if !ok || metrics.LossPercent >= 100 {
		s.lost++
		return
	}
	s.successes++
	s.latestLatency = metrics.LatencyMs
	s.totalLatency += metrics.LatencyMs
}

func (s *pingSampler) snapshot() pingMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.samples == 0 {
		return pingMetrics{}
	}
	latency := s.latestLatency
	if s.successes > 0 {
		latency = roundMetric(s.totalLatency / float64(s.successes))
	}
	return pingMetrics{
		LatencyMs:   latency,
		LossPercent: roundMetric(float64(s.lost) / float64(s.samples) * 100),
		Samples:     s.samples,
	}
}

func runPingOnce(ctx context.Context, host string) (pingMetrics, bool) {
	if _, err := exec.LookPath("ping"); err != nil {
		return pingMetrics{}, false
	}
	out, err := runBoundedCommand(ctx, "ping", "-c", "1", "-W", "1", host)
	metrics, ok := parsePingMetrics(string(out))
	if err != nil && !ok {
		return pingMetrics{LossPercent: 100, Samples: 1}, false
	}
	return metrics, ok
}

func startPingSampler(ctx context.Context, host string) *pingSampler {
	if _, err := exec.LookPath("ping"); err != nil {
		return nil
	}
	sampler := &pingSampler{}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			sampleCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			metrics, ok := runPingOnce(sampleCtx, host)
			cancel()
			sampler.record(metrics, ok)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return sampler
}

func attachPingMetricsToInterval(interval *iperf3Interval, metrics pingMetrics) {
	if metrics.Samples <= 0 {
		return
	}
	interval.LatencyMs = metrics.LatencyMs
	interval.LossPercent = metrics.LossPercent
	interval.PingSamples = metrics.Samples
}

func attachPingMetricsToSummary(summary *iperf3Summary, metrics pingMetrics) {
	if metrics.Samples <= 0 {
		return
	}
	summary.LatencyMs = metrics.LatencyMs
	summary.LossPercent = metrics.LossPercent
	summary.PingSamples = metrics.Samples
}

func parseIperf3TextSummary(output string) (iperf3Summary, error) {
	var sent *iperf3Interval
	var received *iperf3Interval
	for _, line := range strings.Split(output, "\n") {
		interval, ok := parseIperf3IntervalLine(line)
		if !ok {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "sender") {
			copy := interval
			sent = &copy
		}
		if strings.Contains(lower, "receiver") {
			copy := interval
			received = &copy
		}
	}
	if sent == nil && received == nil {
		return iperf3Summary{}, fmt.Errorf("iperf3 输出中没有 summary 数据")
	}
	summary := iperf3Summary{}
	if sent != nil {
		summary.SentMbps = sent.Mbps
		summary.SentBytes = sent.TransferBytes
		summary.Seconds = sent.EndSeconds - sent.StartSeconds
		summary.Retransmits = sent.Retransmits
	}
	if received != nil {
		summary.ReceivedMbps = received.Mbps
		summary.ReceivedBytes = received.TransferBytes
		if summary.Seconds <= 0 {
			summary.Seconds = received.EndSeconds - received.StartSeconds
		}
	}
	return summary, nil
}

func shouldSendIperf3Progress(line string, interval iperf3Interval, parallel int) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "sender") || strings.Contains(lower, "receiver") {
		return false
	}
	if interval.EndSeconds <= interval.StartSeconds {
		return false
	}
	if parallel > 1 && interval.Stream != "SUM" {
		return false
	}
	return true
}

func sendIperf3Progress(conn *websocket.Conn, secret, requestID string, interval iperf3Interval) {
	if strings.TrimSpace(interval.TestID) == "" {
		return
	}
	sendResponse(conn, secret, commandResponse{
		Type:      "Iperf3Progress",
		Success:   true,
		Message:   "progress",
		Data:      interval,
		RequestID: requestID,
	})
}

func startIperf3Server(raw json.RawMessage) (map[string]any, error) {
	var req struct {
		Port            int `json:"port"`
		DurationSeconds int `json:"durationSeconds"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.Port <= 0 {
		return nil, fmt.Errorf("port is required")
	}
	if req.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 10
	}
	if _, err := exec.LookPath("iperf3"); err != nil {
		return nil, fmt.Errorf("iperf3 未安装: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.DurationSeconds+20)*time.Second)
	cmd := exec.CommandContext(ctx, "iperf3", "-s", "-1", "-p", strconv.Itoa(req.Port))
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		cancel()
	}()

	return map[string]any{
		"port": req.Port,
		"pid":  cmd.Process.Pid,
	}, nil
}

func runIperf3Client(conn *websocket.Conn, secret string, raw json.RawMessage, requestID string) (map[string]any, error) {
	var req struct {
		TestID          string `json:"testId"`
		Host            string `json:"host"`
		Port            int    `json:"port"`
		DurationSeconds int    `json:"durationSeconds"`
		Parallel        int    `json:"parallel"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if req.Port <= 0 {
		return nil, fmt.Errorf("port is required")
	}
	if req.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 10
	}
	if req.Parallel <= 0 {
		req.Parallel = 1
	}
	if _, err := exec.LookPath("iperf3"); err != nil {
		return nil, fmt.Errorf("iperf3 未安装: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.DurationSeconds+20)*time.Second)
	defer cancel()
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	pings := startPingSampler(pingCtx, req.Host)
	args := []string{
		"-c", req.Host,
		"-p", strconv.Itoa(req.Port),
		"-t", strconv.Itoa(req.DurationSeconds),
		"-P", strconv.Itoa(req.Parallel),
		"-i", "1",
		"-f", "m",
		"--forceflush",
	}
	cmd := exec.CommandContext(ctx, "iperf3", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// stderr 有界捕获：异常进程刷屏不会撑爆内存。
	stderr := &boundedCommandBuffer{limit: maxIperf3OutputBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var output strings.Builder
	scanErr := scanIperf3Output(stdout, &output, func(line string) {
		interval, ok := parseIperf3IntervalLine(line)
		if !ok || !shouldSendIperf3Progress(line, interval, req.Parallel) {
			return
		}
		interval.TestID = req.TestID
		if pings != nil {
			attachPingMetricsToInterval(&interval, pings.snapshot())
		}
		sendIperf3Progress(conn, secret, requestID, interval)
	})
	err = cmd.Wait()
	stopPing()
	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{"output": output.String()}, fmt.Errorf("iperf3 执行超时")
	}
	if scanErr != nil {
		return map[string]any{"output": output.String()}, scanErr
	}
	if err != nil {
		stderrBytes, _ := stderr.result()
		combined := strings.TrimSpace(strings.TrimSpace(output.String()) + "\n" + strings.TrimSpace(string(stderrBytes)))
		return map[string]any{"output": output.String()}, fmt.Errorf("%v: %s", err, combined)
	}
	summary, err := parseIperf3TextSummary(output.String())
	if err != nil {
		return map[string]any{"output": output.String()}, err
	}
	if pings != nil {
		attachPingMetricsToSummary(&summary, pings.snapshot())
	}
	return map[string]any{
		"summary": summary,
		"output":  output.String(),
	}, nil
}

// maxIperf3OutputBytes 限制 iperf3 文本输出的累计量（1 MiB）：
// 正常测试远小于此值，异常进程不会撑爆内存。
const maxIperf3OutputBytes = 1 << 20

func scanIperf3Output(stdout io.Reader, output *strings.Builder, onLine func(string)) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if output.Len() < maxIperf3OutputBytes {
			remaining := maxIperf3OutputBytes - output.Len()
			if len(line)+1 > remaining {
				line = line[:max(0, remaining-1)]
			}
			output.WriteString(line)
			output.WriteByte('\n')
		}
		onLine(scanner.Text())
	}
	return scanner.Err()
}
