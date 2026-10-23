package socket

import (
	"os"
	"testing"

	"github.com/go-gost/x/config"
)

func TestCommandPersistsConfigSkipsReadOnlyCommands(t *testing.T) {
	for _, command := range []string{"TcpPing", "Unknown", ""} {
		if commandPersistsConfig(command) {
			t.Fatalf("%q must not rewrite gost.json", command)
		}
	}
	for _, command := range []string{"AddService", "UpdateService", "DeleteService", "PauseService", "ResumeService",
		"ReconcileManagedRuntime", "AddChains", "UpdateChains", "DeleteChains", "AddLimiters", "UpdateLimiters",
		"DeleteLimiters", "SetProtocol", "UpgradeNode"} {
		if !commandPersistsConfig(command) {
			t.Fatalf("%q must persist config", command)
		}
	}
}

func TestRouteCommandDoesNotRewriteConfigForDiagnostics(t *testing.T) {
	t.Chdir(t.TempDir())
	original := config.Global()
	t.Cleanup(func() { config.Set(original) })
	config.Set(&config.Config{})
	w := &WebSocketReporter{}
	for _, tc := range []struct {
		name    string
		command CommandMessage
		persist bool
	}{
		{"diagnosis result", CommandMessage{Type: "TcpPing", Data: map[string]interface{}{"ip": "127.0.0.1", "port": 0}}, false},
		{"diagnosis decode error", CommandMessage{Type: "TcpPing", Data: "invalid payload"}, false},
		{"unknown command", CommandMessage{Type: "Unknown"}, false},
		// 修改类命令可能先改变部分运行状态再报错，仍保留原有落盘行为。
		{"mutation error", CommandMessage{Type: "AddService", Data: "invalid payload"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const originalFile = "existing configuration must not be truncated"
			if err := os.WriteFile("gost.json", []byte(originalFile), 0600); err != nil {
				t.Fatal(err)
			}
			w.routeCommand(tc.command)
			got, err := os.ReadFile("gost.json")
			if err != nil {
				t.Fatal(err)
			}
			if changed := string(got) != originalFile; changed != tc.persist {
				t.Fatalf("config rewritten=%v, want %v: %q", changed, tc.persist, got)
			}
		})
	}
}
