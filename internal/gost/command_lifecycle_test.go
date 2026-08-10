package gost

import (
	"reflect"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestRemoteServiceLifecycleUsesNoDeadlineSenderAndPreservesPayload(t *testing.T) {
	original := sendLifecycleMessage
	t.Cleanup(func() { sendLifecycleMessage = original })

	called := make(chan struct{})
	release := make(chan struct{})
	sendLifecycleMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID != 7 || command != "AddService" {
			t.Fatalf("send=(node=%d command=%q)", nodeID, command)
		}
		services, ok := data.([]J)
		if !ok || len(services) != 1 {
			t.Fatalf("payload=%#v", data)
		}
		service := services[0]
		if service["name"] != "fp_7_tls" || service["addr"] != ":28080" {
			t.Fatalf("service=%#v", service)
		}
		close(called)
		<-release
		return ws.GostResult{Msg: SuccessMsg}
	}

	done := make(chan ws.GostResult, 1)
	go func() { done <- AddRemoteServiceLifecycle(7, "fp_7", 28080, "198.51.100.7:443", "relay", "fifo", "") }()
	<-called
	select {
	case result := <-done:
		t.Fatalf("lifecycle sender returned before healthy delayed response: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-done:
		if !IsOK(result) {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle sender did not deliver eventual response")
	}
}

func TestLimiterLifecyclePreservesCommandsPayloadsAndUnknown(t *testing.T) {
	original := sendLifecycleMessage
	t.Cleanup(func() { sendLifecycleMessage = original })
	type call struct {
		command string
		data    interface{}
	}
	var calls []call
	sendLifecycleMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID != 9 {
			t.Fatalf("node=%d", nodeID)
		}
		calls = append(calls, call{command: command, data: data})
		return ws.GostResult{Msg: "节点连接已替换", OutcomeUnknown: true}
	}
	results := []ws.GostResult{
		AddLimitersLifecycle(9, 12, "1.5"),
		UpdateLimitersLifecycle(9, 12, "2"),
		DeleteLimitersLifecycle(9, 12),
	}
	for _, got := range results {
		if !got.OutcomeUnknown || got.Msg != "节点连接已替换" {
			t.Fatalf("result=%+v", got)
		}
	}
	want := []call{
		{command: "AddLimiters", data: limiterData(12, "1.5")},
		{command: "UpdateLimiters", data: J{"limiter": "12", "data": limiterData(12, "2")}},
		{command: "DeleteLimiters", data: J{"limiter": "12"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%#v want=%#v", calls, want)
	}
}

func TestRemoteServiceLifecyclePropagatesDisconnectAndReplacementUnknown(t *testing.T) {
	for _, message := range []string{"节点连接已断开", "节点连接已替换"} {
		t.Run(message, func(t *testing.T) {
			original := sendLifecycleMessage
			t.Cleanup(func() { sendLifecycleMessage = original })
			sendLifecycleMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
				return ws.GostResult{Msg: message, OutcomeUnknown: true}
			}
			add := AddRemoteServiceLifecycle(8, "fp_8", 28081, "198.51.100.8:443", "relay", "fifo", "")
			if add.Msg != message || !add.OutcomeUnknown {
				t.Fatalf("add result=%+v", add)
			}
			remove := DeleteRemoteServiceLifecycle(8, "fp_8")
			if remove.Msg != message || !remove.OutcomeUnknown {
				t.Fatalf("delete result=%+v", remove)
			}
		})
	}
}

func TestForwardMutationLifecycleVariantsUseLifecycleSender(t *testing.T) {
	original := sendLifecycleMessage
	t.Cleanup(func() { sendLifecycleMessage = original })
	type call struct {
		command string
		data    interface{}
	}
	var calls []call
	sendLifecycleMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID != 18 {
			t.Fatalf("node=%d", nodeID)
		}
		calls = append(calls, call{command: command, data: data})
		return ws.GostResult{Msg: SuccessMsg}
	}
	DeleteServiceLifecycle(18, "fp_18")
	PauseServiceLifecycle(18, "fp_18")
	ResumeRemoteServiceLifecycle(18, "fp_18")
	DeleteChainsLifecycle(18, "fp_18")
	want := []call{
		{command: "DeleteService", data: J{"services": []string{"fp_18_tcp", "fp_18_udp"}}},
		{command: "PauseService", data: J{"services": []string{"fp_18_tcp", "fp_18_udp"}}},
		{command: "ResumeService", data: J{"services": []string{"fp_18_tls"}}},
		{command: "DeleteChains", data: J{"chain": "fp_18_chains"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%#v want=%#v", calls, want)
	}
}

func TestManagedRuntimeReconcileUsesLifecycleSender(t *testing.T) {
	original := sendLifecycleMessage
	t.Cleanup(func() { sendLifecycleMessage = original })
	sendLifecycleMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID != 19 || command != "ReconcileManagedRuntime" {
			t.Fatalf("send=(node=%d command=%q)", nodeID, command)
		}
		want := J{"services": []string{"1_2_3_tcp"}, "chains": []string{"1_2_3_chains"}}
		if !reflect.DeepEqual(data, want) {
			t.Fatalf("payload=%#v want=%#v", data, want)
		}
		return ws.GostResult{Msg: SuccessMsg}
	}
	if got := ReconcileManagedRuntimeLifecycle(19, []string{"1_2_3_tcp"}, []string{"1_2_3_chains"}); !IsOK(got) {
		t.Fatalf("result=%+v", got)
	}
}
