package socket

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-gost/x/config"
)

func TestPlanManagedRuntimeDeletesOnlyStrictManagedStaleNames(t *testing.T) {
	plan, err := planManagedRuntimeReconciliation(
		[]string{"1_2_3_tcp", "1_2_3_udp", "1_2_0_tls", "custom-api", "1_2_x_tcp"},
		[]string{"1_2_3_chains", "custom-chain"},
		reconcileManagedRuntimeRequest{Services: []string{"1_2_3_tcp", "1_2_0_tls"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.staleServices, ","), "1_2_3_udp"; got != want {
		t.Fatalf("stale services=%q want=%q", got, want)
	}
	if got, want := strings.Join(plan.staleChains, ","), "1_2_3_chains"; got != want {
		t.Fatalf("stale chains=%q want=%q", got, want)
	}
}

func TestPlanManagedRuntimeIncludesRegistryOnlyOrphan(t *testing.T) {
	// localServices is the union of config and registry names; this name models
	// a live registry object missing from persisted config.
	plan, err := planManagedRuntimeReconciliation([]string{"9_8_7_tcp"}, nil, reconcileManagedRuntimeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.staleServices, []string{"9_8_7_tcp"}) {
		t.Fatalf("registry-only stale=%v", plan.staleServices)
	}
}

func TestPlanManagedRuntimeRejectsInvalidDuplicateAndOversizeDesired(t *testing.T) {
	for _, req := range []reconcileManagedRuntimeRequest{
		{Services: []string{"custom-api"}},
		{Services: []string{"1_2_3_tcp", "1_2_3_tcp"}},
		{Chains: []string{"1_2_3_tcp"}},
	} {
		if _, err := planManagedRuntimeReconciliation(nil, nil, req); err == nil {
			t.Fatalf("invalid desired accepted: %+v", req)
		}
	}
	many := make([]string, maxManagedRuntimeItems+1)
	for i := range many {
		many[i] = "1_2_3_tcp"
	}
	if _, err := planManagedRuntimeReconciliation(nil, nil, reconcileManagedRuntimeRequest{Services: many}); err == nil {
		t.Fatal("oversize desired accepted")
	}
}

func TestDecodeManagedRuntimeRejectsUnknownFieldsAndOversizePayload(t *testing.T) {
	if _, err := decodeReconcileManagedRuntimeRequest(map[string]interface{}{"services": []string{}, "unknown": true}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := decodeReconcileManagedRuntimeRequest(map[string]interface{}{"services": []string{strings.Repeat("1", maxManagedRuntimePayload)}}); err == nil {
		t.Fatal("oversize payload accepted")
	}
}

func TestRemoveManagedRuntimeConfigPreservesUnmanagedObjects(t *testing.T) {
	original := config.Global()
	t.Cleanup(func() { config.Set(original) })
	config.Set(&config.Config{
		Services: []*config.ServiceConfig{
			{Name: "1_2_3_tcp"},
			{Name: "custom-api"},
		},
		Chains: []*config.ChainConfig{
			{Name: "1_2_3_chains"},
			{Name: "custom-chain"},
		},
	})
	if err := removeManagedRuntimeConfig([]string{"1_2_3_tcp"}, []string{"1_2_3_chains"}); err != nil {
		t.Fatal(err)
	}
	got := config.Global()
	if len(got.Services) != 1 || got.Services[0].Name != "custom-api" {
		t.Fatalf("services=%+v", got.Services)
	}
	if len(got.Chains) != 1 || got.Chains[0].Name != "custom-chain" {
		t.Fatalf("chains=%+v", got.Chains)
	}
}
