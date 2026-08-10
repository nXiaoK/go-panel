package service

import (
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
)

func TestGostServiceCleanupCandidatesDeduplicatesPairedServices(t *testing.T) {
	candidates := gostServiceCleanupCandidates([]dto.ConfigItem{
		{Name: "10_20_30_tcp"},
		{Name: "10_20_30_udp"},
		{Name: "10_20_30_tls"},
		{Name: "10_20_30_tls"},
		{Name: "web_api"},
		{Name: "10_20_30_invalid"},
		{Name: "malformed"},
	})

	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2: %#v", len(candidates), candidates)
	}
	if got := candidates[0]; got.serviceName != "10_20_30" || got.forwardID != "10" || got.remote {
		t.Fatalf("main candidate = %#v", got)
	}
	if got := candidates[1]; got.serviceName != "10_20_30" || got.forwardID != "10" || !got.remote {
		t.Fatalf("remote candidate = %#v", got)
	}
}
