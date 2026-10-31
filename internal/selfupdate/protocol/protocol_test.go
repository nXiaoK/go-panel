package protocol

import (
	"encoding/json"
	"testing"
)

func TestRangeContainsIsInclusive(t *testing.T) {
	r := Range{Min: 1, Max: 2}
	if !r.Contains(1) || !r.Contains(2) || r.Contains(0) || r.Contains(3) {
		t.Fatalf("range %+v has wrong bounds", r)
	}
}

func TestVersionReportFlattensCompatFields(t *testing.T) {
	raw, err := json.Marshal(VersionReport{
		Version: "v0.2.0",
		Compat:  Compat{RuntimeABI: 1, SupervisorProtocol: Range{Min: 1, Max: 1}, MinUpgradeFrom: "v0.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "commit", "buildTime", "goos", "goarch", "runtimeAbi", "schemaEpoch", "minReaderEpoch", "supervisorProtocol", "minUpgradeFrom"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("version report JSON is missing %q: %s", key, raw)
		}
	}
	if _, nested := fields["Compat"]; nested {
		t.Fatalf("compat fields must be flattened: %s", raw)
	}
}

func TestProtocolConstantsAreStable(t *testing.T) {
	// 这些取值是跨版本契约；测试失败说明有人修改了旧 supervisor 依赖的约定。
	if ExitRestartRequested != 75 || ExitSchemaTooNew != 78 || ReadyFD != 3 || ReadyMessagePrefix != "READY=1" {
		t.Fatal("supervisor protocol constants changed")
	}
	if !(Range{Min: ChildProtocolMin, Max: ChildProtocolMax}).Contains(SupervisorProtocol) {
		t.Fatal("the child must support the supervisor protocol shipped in the same build")
	}
}
