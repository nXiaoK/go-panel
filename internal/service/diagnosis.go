package service

import (
	"errors"
	"sync"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// diagnosisConcurrency 限制同时参与诊断的节点数，而不是同一节点上的并发命令数。
// gost 节点在 WebSocket 读循环中同步执行命令，nft agent 的诊断通道只有 1 个 worker、缓冲 2 条；
// 同一节点并发下发只会排队，排队时间计入面板端 10 秒等待，导致可达目标被误报为超时或“队列已满”。
const diagnosisConcurrency = 4

// diagnosisProbeWait 是面板等待单个 TcpPing 回包的上限，与 ws.SendMsg 的默认等待保持一致。
const diagnosisProbeWait = ws.DefaultSendTimeout

// diagnosisResponseGrace 为数据库查询、JSON 序列化和网络调度保留余量。
const diagnosisResponseGrace = 15 * time.Second

// maxDiagnosisResponseTimeout 限制单次诊断的预计等待窗口；超过预算时在下发前拒绝，
// 不能只截断 HTTP 写期限，否则探测仍继续运行，且最终结果无法返回。
// 前端诊断请求超时（features/*/api.ts）必须不小于该值加 5 秒。
const maxDiagnosisResponseTimeout = 5 * time.Minute

type diagnosisPingFunc func(*model.Node, string, int, string, int, int) DiagnosisResult

// diagnosisBudgetFunc 在探测开始前接收最坏耗时，handler 据此延长写期限。
type diagnosisBudgetFunc func(time.Duration)

type diagnosisProbe struct {
	node        model.Node
	targetIP    string
	port        int
	description string
	count       int
	timeoutMs   int
	advisory    bool
}

// groupDiagnosisProbes 按节点首次出现的顺序分组，每组保留原始下标，供组内串行执行。
func groupDiagnosisProbes(probes []diagnosisProbe) [][]int {
	groups := make([][]int, 0, 3)
	position := make(map[int64]int)
	for index := range probes {
		nodeID := probes[index].node.ID
		slot, ok := position[nodeID]
		if !ok {
			slot = len(groups)
			position[nodeID] = slot
			groups = append(groups, nil)
		}
		groups[slot] = append(groups[slot], index)
	}
	return groups
}

// diagnosisWorstCase 计算最坏耗时：同节点串行，每条最多等待 diagnosisProbeWait；
// 节点组数不超过并发上限时取最长组，否则保守地按全部串行累加（实际诊断最多涉及入口、中继、出口 3 个节点）。
func diagnosisWorstCase(probes []diagnosisProbe) time.Duration {
	groups := groupDiagnosisProbes(probes)
	var longest, total time.Duration
	for _, group := range groups {
		cost := time.Duration(len(group)) * diagnosisProbeWait
		total += cost
		longest = max(longest, cost)
	}
	if len(groups) <= diagnosisConcurrency {
		return longest
	}
	return total
}

// DiagnosisResponseTimeout 返回 HTTP 写期限：最坏耗时加余量，并钳制到 maxDiagnosisResponseTimeout。
func DiagnosisResponseTimeout(worstCase time.Duration) time.Duration {
	return min(worstCase+diagnosisResponseGrace, maxDiagnosisResponseTimeout)
}

// runDiagnosisProbes 同一节点串行、不同节点并发（最多 diagnosisConcurrency 个节点）；
// 结果按原始下标写回，保持链路、出口和目标的展示顺序。
// ping 参数让并发与超时测试不依赖真实节点，也不需要修改全局函数。
func runDiagnosisProbes(probes []diagnosisProbe, ping diagnosisPingFunc) []DiagnosisResult {
	if len(probes) == 0 {
		return nil
	}
	results := make([]DiagnosisResult, len(probes))
	slots := make(chan struct{}, diagnosisConcurrency)
	var workers sync.WaitGroup
	for _, group := range groupDiagnosisProbes(probes) {
		workers.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			for _, index := range group {
				probe := &probes[index]
				results[index] = ping(&probe.node, probe.targetIP, probe.port, probe.description, probe.count, probe.timeoutMs)
				results[index].Advisory = probe.advisory
			}
		})
	}
	workers.Wait()
	return results
}

// runDiagnosisProbesWithBudget 先检查预算再延长写期限；超过 5 分钟（含响应余量）时整次拒绝，
// 不下发部分探测，也不修改已有转发或目标配置。这里的预算按正常发送后的回包等待计算。
func runDiagnosisProbesWithBudget(probes []diagnosisProbe, ping diagnosisPingFunc, onBudget diagnosisBudgetFunc) ([]DiagnosisResult, error) {
	worstCase := diagnosisWorstCase(probes)
	if worstCase > maxDiagnosisResponseTimeout-diagnosisResponseGrace {
		return nil, errors.New("诊断目标过多，预计等待时间超过 5 分钟，请减少单次诊断的目标数量")
	}
	if onBudget != nil {
		onBudget(worstCase)
	}
	return runDiagnosisProbes(probes, ping), nil
}
