package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// 通道屏障直接验证同时在途的节点数，避免用耗时阈值推断并发导致慢机器或 race 模式下误报。
func TestRunDiagnosisProbesSerializesPerNodeAndLimitsParallelNodes(t *testing.T) {
	for _, nodeCount := range []int{3, 7} {
		t.Run(fmt.Sprint(nodeCount), func(t *testing.T) {
			var probes []diagnosisProbe
			for target := range 3 {
				for nodeID := 1; nodeID <= nodeCount; nodeID++ {
					probes = append(probes, diagnosisProbe{node: model.Node{ID: int64(nodeID)}, targetIP: fmt.Sprintf("%d-%d", nodeID, target), advisory: target == 2})
				}
			}
			started := make(chan struct{}, len(probes))
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var mu sync.Mutex
			active, peak := 0, 0
			activeByNode, peakByNode := map[int64]int{}, map[int64]int{}
			done := make(chan []DiagnosisResult, 1)
			go func() {
				done <- runDiagnosisProbes(probes, func(node *model.Node, ip string, _ int, _ string, _, _ int) DiagnosisResult {
					mu.Lock()
					active++
					peak = max(peak, active)
					activeByNode[node.ID]++
					peakByNode[node.ID] = max(peakByNode[node.ID], activeByNode[node.ID])
					mu.Unlock()
					started <- struct{}{}
					<-release
					mu.Lock()
					active--
					activeByNode[node.ID]--
					mu.Unlock()
					return DiagnosisResult{TargetIP: ip, Success: true}
				})
			}()
			wantPeak := min(nodeCount, diagnosisConcurrency)
			for range wantPeak {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("different nodes did not start concurrently")
				}
			}
			unblock()
			select {
			case results := <-done:
				for i, row := range results {
					if !row.Success || row.TargetIP != probes[i].targetIP || row.Advisory != probes[i].advisory {
						t.Fatalf("result %d=%+v, order or advisory changed", i, row)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("workers did not finish")
			}
			mu.Lock()
			defer mu.Unlock()
			if peak != wantPeak {
				t.Fatalf("peak=%d, want %d", peak, wantPeak)
			}
			for id, nodePeak := range peakByNode {
				if nodePeak != 1 {
					t.Fatalf("node %d has %d concurrent probes", id, nodePeak)
				}
			}
		})
	}
}

func TestDiagnosisBudgetRejectsOversizedWorkBeforeDispatch(t *testing.T) {
	for _, count := range []int{28, 29} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			probes := make([]diagnosisProbe, count)
			var calls atomic.Int32
			budgetSet := false
			rows, err := runDiagnosisProbesWithBudget(probes, func(_ *model.Node, _ string, _ int, _ string, _, _ int) DiagnosisResult {
				if !budgetSet {
					t.Error("probe dispatched before extending HTTP deadline")
				}
				calls.Add(1)
				return DiagnosisResult{Success: true}
			}, func(worst time.Duration) {
				budgetSet = true
				if worst != time.Duration(count)*diagnosisProbeWait {
					t.Errorf("budget=%v", worst)
				}
			})
			if count == 28 {
				if err != nil || len(rows) != count || int(calls.Load()) != count {
					t.Fatalf("valid budget: rows=%d calls=%d err=%v", len(rows), calls.Load(), err)
				}
			} else if err == nil || len(rows) != 0 || calls.Load() != 0 || budgetSet {
				t.Fatalf("oversized work dispatched: rows=%d calls=%d budgetSet=%v err=%v", len(rows), calls.Load(), budgetSet, err)
			}
		})
	}
}

func TestDiagnoseForwardRejectsTooManyTargetsWithoutProbing(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	targets := make([]string, 29)
	for i := range targets {
		targets[i] = fmt.Sprintf("target%d.example:443", i)
	}
	forward := model.Forward{UserID: 1, TunnelID: tunnel.ID, Name: "too-many-targets", RemoteAddr: strings.Join(targets, ","), Status: 1}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	res := diagnoseForwardRun(CurrentUser{UserID: 1, RoleID: adminRoleID}, forward.ID, func(_ *model.Node, _ string, _ int, _ string, _, _ int) DiagnosisResult {
		calls.Add(1)
		return DiagnosisResult{}
	}, nil)
	if res.Code == 0 || !strings.Contains(res.Msg, "诊断目标过多") || calls.Load() != 0 {
		t.Fatalf("result=%+v, dispatched=%d", res, calls.Load())
	}
}

func TestDiagnosisWorstCaseAndResponseTimeout(t *testing.T) {
	probe := func(nodeID int64) diagnosisProbe { return diagnosisProbe{node: model.Node{ID: nodeID}} }
	for _, tc := range []struct {
		name   string
		probes []diagnosisProbe
		worst  time.Duration
	}{
		{"empty", nil, 0},
		{"two-node", []diagnosisProbe{probe(1), probe(2), probe(2), probe(2)}, 30 * time.Second},
		{"relay", []diagnosisProbe{probe(1), probe(2), probe(3), probe(3)}, 20 * time.Second},
		{"more-nodes-than-slots", []diagnosisProbe{probe(1), probe(2), probe(3), probe(4), probe(5)}, 50 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosisWorstCase(tc.probes); got != tc.worst {
				t.Fatalf("worst=%v, want %v", got, tc.worst)
			}
		})
	}
	if got := DiagnosisResponseTimeout(30 * time.Second); got != 45*time.Second {
		t.Fatalf("timeout=%v", got)
	}
	if got := DiagnosisResponseTimeout(time.Hour); got != maxDiagnosisResponseTimeout {
		t.Fatalf("timeout not clamped: %v", got)
	}
}

func TestDiagnoseForwardSerializesSameNodeTargetsInOrder(t *testing.T) {
	entry, _, tunnel, _ := setupPortDetectDB(t)
	forward := model.Forward{UserID: 1, TunnelID: tunnel.ID, Name: "diagnosis", RemoteAddr: "first.example:80,timeout.example:81,last.example:82", Status: 1}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	var budget time.Duration
	var inFlight, peak atomic.Int32
	res := diagnoseForwardRun(CurrentUser{UserID: 1, RoleID: adminRoleID}, forward.ID, func(node *model.Node, ip string, port int, description string, _, _ int) DiagnosisResult {
		count := inFlight.Add(1)
		defer inFlight.Add(-1)
		for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
		}
		time.Sleep(10 * time.Millisecond)
		return DiagnosisResult{NodeID: node.ID, TargetIP: ip, TargetPort: port, Description: description, Success: ip != "timeout.example", Message: "模拟超时"}
	}, func(worst time.Duration) { budget = worst })
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	if budget != 3*diagnosisProbeWait {
		t.Fatalf("budget=%v, want 3 serial probes", budget)
	}
	if peak.Load() != 1 {
		t.Fatalf("port-forward targets share the entry node and must be serial, peak=%d", peak.Load())
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
