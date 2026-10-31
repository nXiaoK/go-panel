package versioninfo

import (
	"runtime"
	"testing"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func TestCurrentReportsBuildAndCompat(t *testing.T) {
	report := Current()
	if report.Version != buildinfo.Current().Version || report.GOOS != runtime.GOOS || report.GOARCH != runtime.GOARCH {
		t.Fatalf("unexpected build fields: %+v", report)
	}
	if report.RuntimeABI != buildinfo.RuntimeABI || report.SchemaEpoch != model.SchemaEpoch || report.MinReaderEpoch != model.MinReaderEpoch {
		t.Fatalf("unexpected compat fields: %+v", report.Compat)
	}
	if report.MinReaderEpoch > report.SchemaEpoch {
		t.Fatal("MinReaderEpoch must never exceed SchemaEpoch")
	}
	if !report.SupervisorProtocol.Contains(protocol.SupervisorProtocol) {
		t.Fatal("the child must support the supervisor protocol shipped in the same build")
	}
}
