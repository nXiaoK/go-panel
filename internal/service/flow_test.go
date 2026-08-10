package service

import (
	"math"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
)

// TestScaleFlowConsistency 锁定 #14：gost 与 nftables 两条上报路径共用 scaleFlow，
// 折算口径必须一致（倍率与计费类型一并相乘后单次取整）。
func TestScaleFlowConsistency(t *testing.T) {
	cases := []struct {
		name     string
		in       dto.FlowDto
		ratio    float64
		flowType int
		wantUp   int64
		wantDown int64
	}{
		{"双向计费倍率1", dto.FlowDto{U: 1000, D: 2000}, 1.0, 2, 2000, 4000},
		{"单向计费倍率1", dto.FlowDto{U: 1000, D: 2000}, 1.0, 1, 1000, 2000},
		{"倍率2双向", dto.FlowDto{U: 1000, D: 2000}, 2.0, 2, 4000, 8000},
		{"小数倍率单次取整", dto.FlowDto{U: 333, D: 333}, 1.5, 2, 999, 999},
		{"非法计费类型回退双向", dto.FlowDto{U: 100, D: 100}, 1.0, 0, 200, 200},
		{"负倍率按0处理", dto.FlowDto{U: 100, D: 100}, -1.0, 2, 0, 0},
		{"NaN倍率按0处理", dto.FlowDto{U: 100, D: 100}, math.NaN(), 2, 0, 0},
		{"正无穷倍率截断", dto.FlowDto{U: 100, D: 100}, math.Inf(1), 2, math.MaxInt64, math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaleFlow(tc.in, tc.ratio, tc.flowType)
			if got.U != tc.wantUp || got.D != tc.wantDown {
				t.Fatalf("scaleFlow(%+v, %v, %d) = U:%d D:%d，期望 U:%d D:%d",
					tc.in, tc.ratio, tc.flowType, got.U, got.D, tc.wantUp, tc.wantDown)
			}
		})
	}
}

// TestScaleFlowSingleRounding 验证“单次取整”与旧 gost 的“分两次取整”口径不同，
// 确保两条路径不会因取整点不同而产生统计偏差。
func TestScaleFlowSingleRounding(t *testing.T) {
	// 旧 gost: int64(float64(d)*ratio) * flowType = int64(333*1.5)*2 = 499*2 = 998
	// 新口径: int64(333*1.5*2) = int64(999.0) = 999
	got := scaleFlow(dto.FlowDto{U: 333, D: 333}, 1.5, 2)
	if got.D != 999 {
		t.Fatalf("单次取整应为 999，得到 %d", got.D)
	}
}

func TestParseFlowServiceName(t *testing.T) {
	cases := []struct {
		name             string
		wantForwardID    int64
		wantUserID       int64
		wantUserTunnelID int64
		wantOK           bool
	}{
		{"12_34_0", 12, 34, 0, true},
		{"12_34_56_tcp", 12, 34, 56, true},
		{"12_34_56_udp", 12, 34, 56, true},
		{"12_34_56_tls", 12, 34, 56, true},
		{"12_34_56_http", 0, 0, 0, false},
		{"12_34", 0, 0, 0, false},
		{"12_admin_0", 0, 0, 0, false},
		{"0_34_0", 0, 0, 0, false},
		{"12_34_-1", 0, 0, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseFlowServiceName(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("parseFlowServiceName(%q) ok=%v, want %v", tc.name, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.forwardID != tc.wantForwardID || got.userID != tc.wantUserID || got.userTunnelID != tc.wantUserTunnelID {
				t.Fatalf("parseFlowServiceName(%q) = %+v", tc.name, got)
			}
		})
	}
}

func TestFlowLimitBytesSaturates(t *testing.T) {
	if got := flowLimitBytes(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("flowLimitBytes should saturate, got %d", got)
	}
	if got := totalFlowBytes(math.MaxInt64, 1); got != math.MaxInt64 {
		t.Fatalf("totalFlowBytes should saturate, got %d", got)
	}
}
