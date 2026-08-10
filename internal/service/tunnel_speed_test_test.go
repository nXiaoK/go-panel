package service

import (
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestNormalizeTunnelSpeedTestRequest(t *testing.T) {
	req := normalizeTunnelSpeedTestRequest(dto.TunnelSpeedTestDto{TunnelID: 7})

	if req.TunnelID != 7 {
		t.Fatalf("TunnelID=%d, want 7", req.TunnelID)
	}
	if req.Direction != speedDirectionInToOut {
		t.Fatalf("Direction=%q, want %q", req.Direction, speedDirectionInToOut)
	}
	if req.DurationSeconds != 10 {
		t.Fatalf("DurationSeconds=%d, want 10", req.DurationSeconds)
	}
	if req.Parallel != 1 {
		t.Fatalf("Parallel=%d, want 1", req.Parallel)
	}
	if req.Port < 30000 || req.Port > 39999 {
		t.Fatalf("Port=%d outside auto range", req.Port)
	}
	if req.TestID == "" {
		t.Fatal("TestID should be generated when absent")
	}
}

func TestNormalizeTunnelSpeedTestRequestKeepsProvidedTestID(t *testing.T) {
	req := normalizeTunnelSpeedTestRequest(dto.TunnelSpeedTestDto{TunnelID: 7, TestID: "live-123"})

	if req.TestID != "live-123" {
		t.Fatalf("TestID=%q, want live-123", req.TestID)
	}
}

func TestValidateIperfPort(t *testing.T) {
	for _, port := range []int{1, 30000, 65535} {
		if err := validateIperfPort(port); err != nil {
			t.Fatalf("validateIperfPort(%d) returned error: %v", port, err)
		}
	}
	for _, port := range []int{0, 65536} {
		if err := validateIperfPort(port); err == nil {
			t.Fatalf("validateIperfPort(%d) returned nil, want error", port)
		}
	}
}

func TestSpeedTestNodeIDsForDirection(t *testing.T) {
	tunnel := model.Tunnel{Type: tunnelTypeTunnelForward, InNodeID: 11, OutNodeID: 22}

	src, dst, err := speedTestNodeIDs(tunnel, speedDirectionInToOut)
	if err != nil {
		t.Fatalf("in-to-out returned error: %v", err)
	}
	if src != 11 || dst != 22 {
		t.Fatalf("in-to-out src=%d dst=%d, want 11 -> 22", src, dst)
	}

	src, dst, err = speedTestNodeIDs(tunnel, speedDirectionOutToIn)
	if err != nil {
		t.Fatalf("out-to-in returned error: %v", err)
	}
	if src != 22 || dst != 11 {
		t.Fatalf("out-to-in src=%d dst=%d, want 22 -> 11", src, dst)
	}
}

func TestSpeedTestNodeIDsRejectsPortForwardTunnel(t *testing.T) {
	_, _, err := speedTestNodeIDs(model.Tunnel{Type: tunnelTypePortForward, InNodeID: 11, OutNodeID: 11}, speedDirectionInToOut)
	if err == nil {
		t.Fatal("expected port-forward tunnel to be rejected")
	}
}

func TestParseIperf3ClientJSON(t *testing.T) {
	raw := []byte(`{
		"end": {
			"sum_sent": {"bits_per_second": 943718400, "bytes": 117964800, "seconds": 10.0001, "retransmits": 2},
			"sum_received": {"bits_per_second": 900000000, "bytes": 112500000, "seconds": 10.0001}
		},
		"ping": {
			"latencyMs": 0.42,
			"lossPercent": 12.5
		}
	}`)

	parsed, err := parseIperf3ClientJSON(raw)
	if err != nil {
		t.Fatalf("parseIperf3ClientJSON returned error: %v", err)
	}
	if parsed.SentMbps != 943.72 {
		t.Fatalf("SentMbps=%.2f, want 943.72", parsed.SentMbps)
	}
	if parsed.ReceivedMbps != 900 {
		t.Fatalf("ReceivedMbps=%.2f, want 900", parsed.ReceivedMbps)
	}
	if parsed.Retransmits != 2 {
		t.Fatalf("Retransmits=%d, want 2", parsed.Retransmits)
	}
	if parsed.LatencyMs != 0.42 {
		t.Fatalf("LatencyMs=%.2f, want 0.42", parsed.LatencyMs)
	}
	if parsed.LossPercent != 12.5 {
		t.Fatalf("LossPercent=%.2f, want 12.5", parsed.LossPercent)
	}
}
