package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type panelResponse struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type nftRule struct {
	Rule string `json:"rule"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: nft_rule_payload <input-json> <output-rules>")
		os.Exit(2)
	}

	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		os.Exit(1)
	}

	rules, err := extractRules(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse rules: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	for _, rule := range rules {
		if rule == "" {
			continue
		}
		if _, err := fmt.Fprintln(f, rule); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v\n", err)
			os.Exit(1)
		}
	}
}

func extractRules(raw []byte) ([]string, error) {
	var resp panelResponse
	if err := json.Unmarshal(raw, &resp); err == nil {
		if resp.Code != nil && *resp.Code != 0 {
			message := strings.TrimSpace(resp.Msg)
			if message == "" {
				message = "unknown panel error"
			}
			return nil, fmt.Errorf("panel nft config rejected (code %d): %s", *resp.Code, message)
		}
		trimmed := bytes.TrimSpace(resp.Data)
		if resp.Code != nil && (len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))) {
			return nil, fmt.Errorf("panel nft config response has no rule data")
		}
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			return extractRulesFromData(resp.Data)
		}
	}
	return extractRulesFromData(raw)
}

func extractRulesFromData(raw []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("nft rule data must not be empty or null")
	}
	var rows []nftRule
	if err := json.Unmarshal(trimmed, &rows); err == nil {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.Rule)
		}
		return out, nil
	}

	var wrapper struct {
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err == nil && wrapper.Rules != nil {
		return wrapper.Rules, nil
	}

	var plain []string
	if err := json.Unmarshal(trimmed, &plain); err != nil {
		return nil, err
	}
	return plain, nil
}
