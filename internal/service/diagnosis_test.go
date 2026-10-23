package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestNormalizeSpeedTestDuration(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{0, 10}, {-1, 10}, {1, 1}, {10, 10}, {120, 120}, {121, 120}} {
		t.Run(fmt.Sprint(tc.input), func(t *testing.T) {
			if got := NormalizeSpeedTestDuration(tc.input); got != tc.want {
				t.Fatalf("NormalizeSpeedTestDuration(%d)=%d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestRunDiagnosisProbesLimitsConcurrency(t *testing.T) {
	probes := make([]diagnosisProbe, 11)
	for index := range probes {
		probes[index].targetIP = fmt.Sprint(index)
	}
	var active, peak atomic.Int32
	results := runDiagnosisProbes(probes, func(_ *model.Node, ip string, _ int, _ string, _, _ int) DiagnosisResult {
		count := active.Add(1)
		for previous := peak.Load(); count > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, count) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return DiagnosisResult{TargetIP: ip}
	})
	if peak.Load() != diagnosisConcurrency {
		t.Fatalf("peak concurrency=%d, want %d", peak.Load(), diagnosisConcurrency)
	}
	for index, row := range results {
		if row.TargetIP != fmt.Sprint(index) {
			t.Fatalf("result %d=%+v", index, row)
		}
	}
}

func TestDiagnoseForwardRunsTargetsConcurrentlyInOrder(t *testing.T) {
	entry, _, tunnel, _ := setupPortDetectDB(t)
	forward := model.Forward{UserID: 1, TunnelID: tunnel.ID, Name: "diagnosis", RemoteAddr: "first.example:80,timeout.example:81,last.example:82", Status: 1}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	res := diagnoseForwardWithPing(CurrentUser{UserID: 1, RoleID: adminRoleID}, forward.ID, func(node *model.Node, ip string, port int, description string, _, _ int) DiagnosisResult {
		// 三个目标各模拟 150ms 等待，第二项模拟节点超时；串行至少需要 450ms。
		time.Sleep(150 * time.Millisecond)
		return DiagnosisResult{NodeID: node.ID, TargetIP: ip, TargetPort: port, Description: description, Success: ip != "timeout.example", Message: "模拟超时"}
	})
	elapsed := time.Since(started)
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	rows := res.Data.(map[string]interface{})["results"].([]DiagnosisResult)
	if len(rows) != 3 {
		t.Fatalf("results=%+v", rows)
	}
	for index, target := range []string{"first.example", "timeout.example", "last.example"} {
		if rows[index].TargetIP != target || rows[index].TargetPort != 80+index || rows[index].NodeID != entry.ID || rows[index].Success != (index != 1) {
			t.Fatalf("result %d=%+v", index, rows[index])
		}
	}
	if elapsed >= 350*time.Millisecond {
		t.Fatalf("concurrent targets took %v, expected below 350ms versus 450ms serial", elapsed)
	}
}

func TestDiagnoseTunnelPreservesRelayOrder(t *testing.T) {
	fx := setupNftRelayFixture(t)
	res := diagnoseTunnelWithPing(fx.tunnel.ID, func(node *model.Node, ip string, port int, description string, _, _ int) DiagnosisResult {
		if node.ID == fx.entry.ID {
			time.Sleep(30 * time.Millisecond)
		}
		return DiagnosisResult{NodeID: node.ID, TargetIP: ip, TargetPort: port, Description: description}
	})
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	rows := res.Data.(map[string]interface{})["results"].([]DiagnosisResult)
	if len(rows) != 3 {
		t.Fatalf("results=%+v", rows)
	}
	for index, expected := range []struct {
		nodeID      int64
		description string
	}{{fx.entry.ID, "入口->中继"}, {fx.relay.ID, "中继->出口"}, {fx.exit.ID, "出口->外网"}} {
		if rows[index].NodeID != expected.nodeID || rows[index].Description != expected.description {
			t.Fatalf("result %d=%+v", index, rows[index])
		}
	}
}

func TestTunnelInternetProbeUsesConfiguredTargetAndIsAdvisory(t *testing.T) {
	_, _, portTunnel, tunnel := setupPortDetectDB(t)
	ping := func(node *model.Node, ip string, port int, description string, _, _ int) DiagnosisResult {
		return DiagnosisResult{NodeID: node.ID, TargetIP: ip, TargetPort: port, Description: description, Success: false}
	}
	for _, tc := range []struct {
		value, host string
		port        int
	}{
		{"", "www.google.com", 443}, {"probe.example:8443", "probe.example", 8443}, {"[2001:db8::1]:9443", "2001:db8::1", 9443},
	} {
		if res := UpdateConfigs(map[string]string{diagnoseInternetProbeConfigName: tc.value}); res.Code != 0 {
			t.Fatal(res.Msg)
		}
		for _, current := range []*model.Tunnel{portTunnel, tunnel} {
			res := diagnoseTunnelWithPing(current.ID, ping)
			if res.Code != 0 {
				t.Fatal(res.Msg)
			}
			rows := res.Data.(map[string]interface{})["results"].([]DiagnosisResult)
			internet := rows[len(rows)-1]
			if internet.TargetIP != tc.host || internet.TargetPort != tc.port || !internet.Advisory {
				t.Fatalf("internet probe=%+v", internet)
			}
			for _, row := range rows[:len(rows)-1] {
				if row.Advisory {
					t.Fatalf("core probe marked advisory: %+v", row)
				}
			}
			encoded, err := json.Marshal(internet)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"advisory":true`) {
				t.Fatalf("missing advisory JSON: %s", encoded)
			}
		}
	}
}

func TestRelayInternetProbeIsOnlyAdvisoryStep(t *testing.T) {
	fx := setupNftRelayFixture(t)
	res := diagnoseTunnelWithPing(fx.tunnel.ID, func(_ *model.Node, _ string, _ int, _ string, _, _ int) DiagnosisResult { return DiagnosisResult{} })
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	rows := res.Data.(map[string]interface{})["results"].([]DiagnosisResult)
	if len(rows) != 3 || rows[0].Advisory || rows[1].Advisory || !rows[2].Advisory {
		t.Fatalf("rows=%+v", rows)
	}
}
