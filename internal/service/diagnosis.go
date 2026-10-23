package service

import (
	"sync"

	"github.com/nXiaoK/go-panel/internal/model"
)

// diagnosisConcurrency 每次诊断最多同时发送 4 个 TcpPing，避免多目标占满节点命令队列。
// 单个 TcpPing 仍沿用 WebSocket 的 10 秒等待上限，不调整节点或其他请求的全局超时。
const diagnosisConcurrency = 4

type diagnosisPingFunc func(*model.Node, string, int, string, int, int) DiagnosisResult

type diagnosisProbe struct {
	node        model.Node
	targetIP    string
	port        int
	description string
	count       int
	timeoutMs   int
}

// runDiagnosisProbes 先按展示顺序分配结果位置，再由有限 worker 执行；
// 各 worker 只写自己的位置，即使某个目标超时，结果仍保持原链路、出口和目标顺序。
// ping 参数让并发与超时测试不依赖真实节点，也不需要修改全局函数。
func runDiagnosisProbes(probes []diagnosisProbe, ping diagnosisPingFunc) []DiagnosisResult {
	if len(probes) == 0 {
		return nil
	}
	results := make([]DiagnosisResult, len(probes))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(diagnosisConcurrency, len(probes)) {
		workers.Go(func() {
			for index := range jobs {
				probe := &probes[index]
				results[index] = ping(&probe.node, probe.targetIP, probe.port, probe.description, probe.count, probe.timeoutMs)
			}
		})
	}
	for index := range probes {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results
}
