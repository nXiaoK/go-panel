package service

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

const (
	speedDirectionInToOut    = "in-to-out"
	speedDirectionOutToIn    = "out-to-in"
	speedDirectionInToRelay  = "in-to-relay"
	speedDirectionRelayToOut = "relay-to-out"
	speedDirectionOutToRelay = "out-to-relay"
	speedDirectionRelayToIn  = "relay-to-in"
)

type Iperf3Summary struct {
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

type TunnelSpeedTestResult struct {
	TunnelID        int64           `json:"tunnelId"`
	TestID          string          `json:"testId"`
	TunnelName      string          `json:"tunnelName"`
	Direction       string          `json:"direction"`
	SourceNodeID    int64           `json:"sourceNodeId"`
	SourceNodeName  string          `json:"sourceNodeName"`
	TargetNodeID    int64           `json:"targetNodeId"`
	TargetNodeName  string          `json:"targetNodeName"`
	TargetHost      string          `json:"targetHost"`
	Port            int             `json:"port"`
	DurationSeconds int             `json:"durationSeconds"`
	Parallel        int             `json:"parallel"`
	Summary         Iperf3Summary   `json:"summary"`
	Raw             json.RawMessage `json:"raw,omitempty"`
	Timestamp       int64           `json:"timestamp"`
}

func normalizeTunnelSpeedTestRequest(req dto.TunnelSpeedTestDto) dto.TunnelSpeedTestDto {
	req.TestID = strings.TrimSpace(req.TestID)
	if req.TestID == "" {
		req.TestID = uuid.NewString()
	}
	switch req.Direction {
	case speedDirectionOutToIn, speedDirectionInToRelay, speedDirectionRelayToOut, speedDirectionOutToRelay, speedDirectionRelayToIn:
	default:
		req.Direction = speedDirectionInToOut
	}
	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 10
	}
	if req.DurationSeconds > 120 {
		req.DurationSeconds = 120
	}
	if req.Parallel <= 0 {
		req.Parallel = 1
	}
	if req.Parallel > 32 {
		req.Parallel = 32
	}
	if req.Port <= 0 {
		req.Port = 30000 + int(time.Now().UnixNano()%10000)
	}
	return req
}

func validateIperfPort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("iperf3 端口范围必须是 1-65535")
	}
	return nil
}

func speedTestNodeIDs(tunnel model.Tunnel, direction string) (int64, int64, error) {
	if tunnel.Type != tunnelTypeTunnelForward {
		return 0, 0, errors.New("压力测试仅支持隧道转发")
	}
	if tunnel.InNodeID == 0 || tunnel.OutNodeID == 0 || tunnel.InNodeID == tunnel.OutNodeID {
		return 0, 0, errors.New("隧道节点配置不完整")
	}
	if relayNodeID := tunnelRelayNodeID(&tunnel); relayNodeID > 0 {
		if relayNodeID == tunnel.InNodeID || relayNodeID == tunnel.OutNodeID {
			return 0, 0, errors.New("三节点隧道配置不完整")
		}
		switch direction {
		case speedDirectionInToOut, speedDirectionOutToIn:
			return 0, 0, errors.New("三节点串联不支持端到端直测，请选择具体相邻节点方向")
		case speedDirectionRelayToOut:
			return relayNodeID, tunnel.OutNodeID, nil
		case speedDirectionOutToRelay:
			return tunnel.OutNodeID, relayNodeID, nil
		case speedDirectionRelayToIn:
			return relayNodeID, tunnel.InNodeID, nil
		default:
			return tunnel.InNodeID, relayNodeID, nil
		}
	}
	if direction == speedDirectionOutToIn {
		return tunnel.OutNodeID, tunnel.InNodeID, nil
	}
	return tunnel.InNodeID, tunnel.OutNodeID, nil
}

func firstReachableNodeHost(node model.Node) string {
	if strings.TrimSpace(node.ServerIP) != "" {
		return strings.TrimSpace(node.ServerIP)
	}
	for _, part := range strings.Split(node.IP, ",") {
		if host := strings.TrimSpace(part); host != "" {
			return host
		}
	}
	return ""
}

func roundMbps(bitsPerSecond float64) float64 {
	return math.Round(bitsPerSecond/10000) / 100
}

func parseIperf3ClientJSON(raw []byte) (Iperf3Summary, error) {
	var doc struct {
		End struct {
			SumSent struct {
				BitsPerSecond float64 `json:"bits_per_second"`
				Bytes         int64   `json:"bytes"`
				Seconds       float64 `json:"seconds"`
				Retransmits   int64   `json:"retransmits"`
			} `json:"sum_sent"`
			SumReceived struct {
				BitsPerSecond float64 `json:"bits_per_second"`
				Bytes         int64   `json:"bytes"`
				Seconds       float64 `json:"seconds"`
			} `json:"sum_received"`
		} `json:"end"`
		Ping struct {
			LatencyMs   float64 `json:"latencyMs"`
			LossPercent float64 `json:"lossPercent"`
			Samples     int     `json:"samples"`
		} `json:"ping"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Iperf3Summary{}, err
	}
	if doc.End.SumSent.BitsPerSecond <= 0 && doc.End.SumReceived.BitsPerSecond <= 0 {
		return Iperf3Summary{}, errors.New("iperf3 输出中没有速度数据")
	}
	seconds := doc.End.SumSent.Seconds
	if seconds <= 0 {
		seconds = doc.End.SumReceived.Seconds
	}
	summary := Iperf3Summary{
		SentMbps:      roundMbps(doc.End.SumSent.BitsPerSecond),
		ReceivedMbps:  roundMbps(doc.End.SumReceived.BitsPerSecond),
		SentBytes:     doc.End.SumSent.Bytes,
		ReceivedBytes: doc.End.SumReceived.Bytes,
		Seconds:       seconds,
		Retransmits:   doc.End.SumSent.Retransmits,
	}
	if doc.Ping.Samples > 0 || doc.Ping.LatencyMs > 0 || doc.Ping.LossPercent > 0 {
		summary.LatencyMs = doc.Ping.LatencyMs
		summary.LossPercent = doc.Ping.LossPercent
		summary.PingSamples = doc.Ping.Samples
		if summary.PingSamples <= 0 {
			summary.PingSamples = 1
		}
	}
	return summary, nil
}

// SpeedTestTunnel 使用 iperf3 对隧道两端节点做 TCP 吞吐测试。
func SpeedTestTunnel(req dto.TunnelSpeedTestDto) result.R {
	req = normalizeTunnelSpeedTestRequest(req)
	if err := validateIperfPort(req.Port); err != nil {
		return result.Err(err.Error())
	}

	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, req.TunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	sourceID, targetID, err := speedTestNodeIDs(tunnel, req.Direction)
	if err != nil {
		return result.Err(err.Error())
	}

	var source, target model.Node
	if err := model.DB.First(&source, sourceID).Error; err != nil {
		return result.Err("源节点不存在")
	}
	if err := model.DB.First(&target, targetID).Error; err != nil {
		return result.Err("目标节点不存在")
	}
	if source.Status != nodeStatusOnline {
		return result.Err("源节点当前离线")
	}
	if target.Status != nodeStatusOnline {
		return result.Err("目标节点当前离线")
	}

	targetHost := firstReachableNodeHost(target)
	if targetHost == "" {
		return result.Err("目标节点缺少可连接 IP")
	}

	serverRes := ws.SendMsgWithTimeout(target.ID, map[string]interface{}{
		"port":            req.Port,
		"durationSeconds": req.DurationSeconds,
	}, "Iperf3Server", 5*time.Second)
	if serverRes.Msg != "OK" {
		return result.Err("启动目标节点 iperf3 server 失败: " + serverRes.Msg)
	}

	time.Sleep(300 * time.Millisecond)
	clientTimeout := time.Duration(req.DurationSeconds+20) * time.Second
	clientRes := ws.SendMsgWithTimeout(source.ID, map[string]interface{}{
		"testId":          req.TestID,
		"host":            targetHost,
		"port":            req.Port,
		"durationSeconds": req.DurationSeconds,
		"parallel":        req.Parallel,
	}, "Iperf3Client", clientTimeout)
	if clientRes.Msg != "OK" {
		return result.Err("执行 iperf3 client 失败: " + clientRes.Msg)
	}

	var payload struct {
		JSON    json.RawMessage `json:"json"`
		Output  string          `json:"output"`
		Summary *Iperf3Summary  `json:"summary"`
	}
	if len(clientRes.Data) > 0 {
		_ = json.Unmarshal(clientRes.Data, &payload)
	}
	raw := payload.JSON
	var summary Iperf3Summary
	if payload.Summary != nil {
		summary = *payload.Summary
	} else {
		if len(raw) == 0 && strings.TrimSpace(payload.Output) != "" {
			raw = []byte(payload.Output)
		}
		if len(raw) == 0 {
			raw = clientRes.Data
		}
		parsed, err := parseIperf3ClientJSON(raw)
		if err != nil {
			return result.Err("解析 iperf3 输出失败: " + err.Error())
		}
		summary = parsed
	}

	return result.Ok(TunnelSpeedTestResult{
		TunnelID:        tunnel.ID,
		TestID:          req.TestID,
		TunnelName:      tunnel.Name,
		Direction:       req.Direction,
		SourceNodeID:    source.ID,
		SourceNodeName:  source.Name,
		TargetNodeID:    target.ID,
		TargetNodeName:  target.Name,
		TargetHost:      targetHost,
		Port:            req.Port,
		DurationSeconds: req.DurationSeconds,
		Parallel:        req.Parallel,
		Summary:         summary,
		Raw:             raw,
		Timestamp:       time.Now().UnixMilli(),
	})
}
