package child

import (
	"net"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func TestFromEnvParsesSupervisorVariables(t *testing.T) {
	env := map[string]string{
		protocol.EnvSupervisorProtocol: "1",
		protocol.EnvRunMode:            " trial ",
		protocol.EnvBuiltinVersion:     "v0.2.0",
		protocol.EnvBuiltinCommit:      "abc1234",
		protocol.EnvBuiltinRuntimeABI:  "1",
		protocol.EnvBuiltinSchemaEpoch: "3",
		protocol.EnvRequestID:          "req-1",
		protocol.EnvRuntimeDir:         "/app/data/runtime",
	}
	info := FromEnv(func(key string) string { return env[key] })
	want := Info{
		Supervised: true, Protocol: 1, RunMode: "trial",
		BuiltinVersion: "v0.2.0", BuiltinCommit: "abc1234",
		BuiltinRuntimeABI: 1, BuiltinSchemaEpoch: 3,
		RequestID: "req-1", RuntimeDir: "/app/data/runtime",
	}
	if info != want {
		t.Fatalf("FromEnv=%+v want %+v", info, want)
	}
}

func TestFromEnvTreatsInvalidProtocolAsUnsupervised(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "abc", "1.5"} {
		info := FromEnv(func(key string) string {
			if key == protocol.EnvSupervisorProtocol {
				return raw
			}
			return ""
		})
		if info.Supervised || info.Protocol != 0 {
			t.Fatalf("protocol %q must not be supervised: %+v", raw, info)
		}
	}
}

func TestNotifierWithoutPipeIsNoop(t *testing.T) {
	t.Setenv(protocol.EnvReadyFD, "")
	notifier := ClaimReadyPipe()
	if notifier.Enabled() {
		t.Fatal("notifier must be disabled without a ready pipe")
	}
	if err := notifier.Notify(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 6365}); err != nil {
		t.Fatal(err)
	}
	var nilNotifier *Notifier
	if nilNotifier.Enabled() || nilNotifier.Notify(nil) != nil {
		t.Fatal("nil notifier must be a no-op")
	}
}
