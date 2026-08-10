package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractRulesAcceptsSuccessfulPanelEnvelope(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"msg":"操作成功","ts":1,"data":[{"rule":"add rule one"},{"rule":"add rule two"}]}`)
	got, err := extractRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"add rule one", "add rule two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules=%q, want %q", got, want)
	}
}

func TestExtractRulesAcceptsSuccessfulEmptyPanelRuleSet(t *testing.T) {
	t.Parallel()

	got, err := extractRules([]byte(`{"code":0,"msg":"操作成功","data":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("rules=%q, want empty", got)
	}
}

func TestExtractRulesRejectsPanelBusinessErrorInsteadOfApplyingEmptyRules(t *testing.T) {
	t.Parallel()

	for _, raw := range [][]byte{
		[]byte(`{"code":-1,"msg":"节点不存在","ts":1,"data":null}`),
		[]byte(`{"code":503,"msg":"","data":[]}`),
	} {
		if _, err := extractRules(raw); err == nil || !strings.Contains(err.Error(), "panel nft config rejected") {
			t.Fatalf("extractRules(%s) error=%v", raw, err)
		}
	}
}

func TestExtractRulesRejectsSuccessfulEnvelopeWithoutData(t *testing.T) {
	t.Parallel()

	for _, raw := range [][]byte{
		[]byte(`{"code":0,"msg":"操作成功","data":null}`),
		[]byte(`{"code":0,"msg":"操作成功"}`),
	} {
		if _, err := extractRules(raw); err == nil || !strings.Contains(err.Error(), "no rule data") {
			t.Fatalf("extractRules(%s) error=%v", raw, err)
		}
	}
}

func TestExtractRulesRejectsBareNullInsteadOfApplyingEmptyRules(t *testing.T) {
	t.Parallel()

	if _, err := extractRules([]byte(`null`)); err == nil || !strings.Contains(err.Error(), "must not be empty or null") {
		t.Fatalf("extractRules(null) error=%v", err)
	}
}

func TestExtractRulesKeepsLegacyRawFormats(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		raw  string
		want []string
	}{
		{raw: `[{"rule":"add rule one"}]`, want: []string{"add rule one"}},
		{raw: `{"rules":["add rule two"]}`, want: []string{"add rule two"}},
		{raw: `["add rule three"]`, want: []string{"add rule three"}},
		{raw: `{"data":[{"rule":"add rule four"}]}`, want: []string{"add rule four"}},
	} {
		got, err := extractRules([]byte(test.raw))
		if err != nil {
			t.Fatalf("extractRules(%s): %v", test.raw, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("extractRules(%s)=%q, want %q", test.raw, got, test.want)
		}
	}
}
