package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

const nftExecTimeout = 5 * time.Second

const (
	maxNftCommandOutput     = 64 << 20
	maxNftInventoryElements = 100_100
	maxNftOutputLines       = 100_100
	maxNftRuleResults       = 100_000
	maxNftOutputLineBytes   = 16 << 10
)

type nftAgentOps struct {
	activeMarkerPath string
	lockPath         string
	readMarker       func(string) (string, error)
	acquireLock      func(string) (func() error, error)
	run              commandRunner
	runStdin         nftStdinRunner
}

func defaultNftAgentOps() nftAgentOps {
	return nftAgentOps{
		activeMarkerPath: nftgeneration.ActiveTableMarkerPath,
		lockPath:         nftgeneration.LockPath,
		readMarker:       nftgeneration.ReadActiveMarker,
		acquireLock:      nftgeneration.AcquireLock,
		run:              runBoundedCommand,
		runStdin:         runBoundedNftStdin,
	}
}

// resolveActiveNftTableWith reads the durable marker and verifies that the exact
// marked table still exists and owns hooks. It deliberately never discovers a
// fallback generation.
func resolveActiveNftTableWith(ctx context.Context, markerPath string, run commandRunner) (string, error) {
	return resolveActiveNftTableWithReader(ctx, markerPath, nftgeneration.ReadActiveMarker, run)
}

func resolveActiveNftTableWithReader(ctx context.Context, markerPath string, readMarker func(string) (string, error), run commandRunner) (string, error) {
	if readMarker == nil || run == nil {
		return "", errors.New("resolve active nft table: incomplete resolver configuration")
	}
	table, err := readMarker(markerPath)
	if err != nil {
		return "", fmt.Errorf("resolve active nft table: %w", err)
	}
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return "", fmt.Errorf("resolve active nft table: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := run(commandCtx, "nft", "-j", "list", "table", "inet", table)
	if err != nil {
		return "", fmt.Errorf("verify active nft table %q: %w, output: %s", table, err, string(output))
	}
	if err := validateActiveNftTableJSON(output, table); err != nil {
		return "", fmt.Errorf("verify active nft table %q: %w", table, err)
	}
	return table, nil
}

type nftAgentJSONElement struct {
	Metainfo json.RawMessage    `json:"metainfo,omitempty"`
	Table    *nftAgentJSONTable `json:"table,omitempty"`
	Chain    json.RawMessage    `json:"chain,omitempty"`
	Rule     json.RawMessage    `json:"rule,omitempty"`
}

type nftAgentJSONTable struct {
	Family  string          `json:"family"`
	Name    string          `json:"name"`
	Handle  json.RawMessage `json:"handle,omitempty"`
	Flags   json.RawMessage `json:"flags,omitempty"`
	Comment json.RawMessage `json:"comment,omitempty"`
}

func validateActiveNftTableJSON(raw []byte, marker string) error {
	if err := nftgeneration.ValidateUniqueJSON(raw); err != nil {
		return fmt.Errorf("decode nft JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("nft JSON root must be an object")
	}
	if !decoder.More() {
		return errors.New("nft JSON has no nftables array")
	}
	key, err := decoder.Token()
	if err != nil || key != "nftables" {
		return errors.New("nft JSON root must contain only nftables")
	}
	opening, err = decoder.Token()
	if err != nil || opening != json.Delim('[') {
		return errors.New("nft JSON nftables value must be an array")
	}
	found := 0
	metainfo := 0
	elements := 0
	for decoder.More() {
		elements++
		if elements > maxNftInventoryElements {
			return fmt.Errorf("nft JSON exceeds %d inventory elements", maxNftInventoryElements)
		}
		var rawElement json.RawMessage
		if err := decoder.Decode(&rawElement); err != nil {
			return fmt.Errorf("decode nft JSON element %d: %w", elements-1, err)
		}
		var element nftAgentJSONElement
		if err := decodeStrictAgentJSON(rawElement, &element); err != nil {
			return fmt.Errorf("decode nft JSON element %d: %w", elements-1, err)
		}
		present := 0
		for _, rawField := range []json.RawMessage{element.Metainfo, element.Chain, element.Rule} {
			if len(rawField) != 0 {
				present++
			}
		}
		if element.Table != nil {
			present++
		}
		if present != 1 {
			return fmt.Errorf("ambiguous nft JSON element %d", elements-1)
		}
		if element.Table == nil {
			var rawObject json.RawMessage
			switch {
			case len(element.Metainfo) != 0:
				rawObject = element.Metainfo
			case len(element.Chain) != 0:
				rawObject = element.Chain
			case len(element.Rule) != 0:
				rawObject = element.Rule
			}
			if err := validateNftJSONObject(rawObject); err != nil {
				return fmt.Errorf("invalid nft JSON element %d: %w", elements-1, err)
			}
			if len(element.Metainfo) != 0 {
				metainfo++
				if metainfo > 1 {
					return errors.New("nft JSON contains duplicate metainfo elements")
				}
			}
			continue
		}
		found++
		if found > 1 {
			return errors.New("nft JSON contains multiple table identities")
		}
		if element.Table.Family != "inet" || element.Table.Name != marker {
			return fmt.Errorf("nft table identity mismatch: family=%q name=%q", element.Table.Family, element.Table.Name)
		}
		if len(element.Table.Handle) != 0 {
			var handle uint64
			if err := decodeStrictAgentJSON(element.Table.Handle, &handle); err != nil || handle == 0 {
				return errors.New("nft table has invalid handle")
			}
		}
		if len(element.Table.Comment) != 0 {
			var comment string
			if err := decodeStrictAgentJSON(element.Table.Comment, &comment); err != nil {
				return errors.New("nft table has invalid comment")
			}
		}
		if len(element.Table.Flags) != 0 {
			if bytes.Equal(bytes.TrimSpace(element.Table.Flags), []byte("null")) {
				return errors.New("nft table has invalid null flags")
			}
			var flags []string
			if err := decodeStrictAgentJSON(element.Table.Flags, &flags); err != nil {
				return fmt.Errorf("decode nft table flags: %w", err)
			}
			if len(flags) != 0 {
				return fmt.Errorf("marked nft table is not active: flags=%v", flags)
			}
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return errors.New("unterminated nftables array")
	}
	if decoder.More() {
		return errors.New("nft JSON root contains unknown fields")
	}
	closing, err = decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("unterminated nft JSON root")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("nft JSON contains trailing data")
	}
	if found != 1 {
		return errors.New("marked nft table is missing")
	}
	return nil
}

func decodeStrictAgentJSON(raw []byte, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("JSON value must not be empty or null")
	}
	if err := nftgeneration.ValidateUniqueJSON(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func validateNftJSONObject(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return errors.New("nft JSON inventory value must be an object")
	}
	return nftgeneration.ValidateUniqueJSON(trimmed)
}

// listNftRules 读取当前 nftables 规则列表
type nftRulesView struct {
	Table string   `json:"table"`
	Rules []string `json:"rules"`
}

func listNftRules() (nftRulesView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return listNftRulesWithOps(ctx, defaultNftAgentOps())
}

func listNftRulesWithOps(ctx context.Context, ops nftAgentOps) (view nftRulesView, err error) {
	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return nftRulesView{}, err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	table, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return nftRulesView{}, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.run(commandCtx, "nft", "-a", "list", "table", "inet", table)
	if err != nil {
		return nftRulesView{}, fmt.Errorf("list nft rules: %v, output: %s", err, string(output))
	}
	rules, err := parseListedNftRules(output, table)
	if err != nil {
		return nftRulesView{}, err
	}
	return nftRulesView{Table: table, Rules: rules}, nil
}

func parseListedNftRules(output []byte, table string) ([]string, error) {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return nil, err
	}
	rules := make([]string, 0)
	inTable := false
	tableSeen := false
	currentChain := ""
	seenRuleHandles := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), maxNftOutputLineBytes)
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxNftOutputLines {
			return nil, fmt.Errorf("nft list output exceeds %d lines", maxNftOutputLines)
		}
		trimmed := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// 检测表开始
		if strings.HasPrefix(trimmed, "table ") {
			matched, headerErr := parseNftObjectHeader(trimmed, "table inet "+table+" {")
			if headerErr != nil || !matched || tableSeen {
				return nil, errors.New("nft list output table identity mismatch")
			}
			tableSeen = true
			inTable = true
			continue
		}

		// 如果在表内，收集规则
		if inTable {
			// 检测链定义（如 chain prerouting）
			if strings.HasPrefix(trimmed, "chain ") {
				fields := strings.Fields(trimmed)
				if len(fields) < 2 || !validNftChain(fields[1]) {
					return nil, errors.New("nft list output has invalid chain")
				}
				matched, headerErr := parseNftObjectHeader(trimmed, "chain "+fields[1]+" {")
				if headerErr != nil || !matched {
					return nil, errors.New("nft list output has malformed chain header")
				}
				currentChain = fields[1]
				continue
			}

			// 检测表结束
			if trimmed == "}" {
				continue
			}
			if strings.HasPrefix(trimmed, "type ") && strings.HasSuffix(trimmed, ";") {
				continue
			}
			if currentChain == "" {
				return nil, errors.New("nft list output rule precedes a validated chain")
			}
			handle, handleErr := parseNftRuleOutputHandle(trimmed)
			if handleErr != nil {
				return nil, handleErr
			}
			key := currentChain + ":" + strconv.Itoa(handle)
			if _, duplicate := seenRuleHandles[key]; duplicate {
				return nil, errors.New("nft list output has duplicate rule handle")
			}
			seenRuleHandles[key] = struct{}{}

			// 收集规则（以 add rule 或包含关键字的规则）
			// 将相对格式转换为绝对格式（添加 "add rule inet flux_panel" 前缀）
			if strings.Contains(trimmed, "dport") || strings.Contains(trimmed, "dnat") {
				if currentChain != "" && !strings.HasPrefix(trimmed, "add rule ") {
					trimmed = fmt.Sprintf("add rule inet %s %s %s", nftgeneration.LegacyTable, currentChain, trimmed)
				}
				rules = append(rules, trimmed)
				if len(rules) > maxNftRuleResults {
					return nil, fmt.Errorf("nft list output exceeds %d rules", maxNftRuleResults)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan nft list output: %w", err)
	}
	if !tableSeen {
		return nil, errors.New("nft list output is missing the active table")
	}

	// 如果没有找到规则，返回空列表
	if len(rules) == 0 {
		return []string{}, nil
	}

	return rules, nil
}

func acquireNftAgentLock(ops nftAgentOps) (func() error, error) {
	if ops.acquireLock == nil {
		return nil, errors.New("acquire nft agent lock: missing lock implementation")
	}
	release, err := ops.acquireLock(ops.lockPath)
	if err != nil {
		if errors.Is(err, nftgeneration.ErrLocked) {
			return nil, fmt.Errorf("%s %w", nftgeneration.RetryableErrorPrefix, err)
		}
		return nil, fmt.Errorf("acquire nft agent lock: %w", err)
	}
	return release, nil
}

func releaseNftAgentLock(release func() error) error {
	if release == nil {
		return nil
	}
	if err := release(); err != nil {
		return fmt.Errorf("release nft agent lock: %w", err)
	}
	return nil
}

func addNftRule(raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return addNftRuleWithOps(ctx, raw, defaultNftAgentOps())
}

func addNftRuleWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (err error) {
	rule, err := parseValidatedAddNftRule(raw)
	if err != nil {
		return err
	}
	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	table, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	return executeValidatedAddNftRule(commandCtx, rule, table, ops.runStdin)
}

func addNftRules(raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return addNftRulesWithOps(ctx, raw, defaultNftAgentOps())
}

func addNftRulesWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (err error) {
	if len(raw) == 0 || len(raw) > nftgeneration.MaxRuleBatchBytes {
		return fmt.Errorf("batch add request must be between 1 and %d bytes", nftgeneration.MaxRuleBatchBytes)
	}
	var req struct {
		ExpectedTable string   `json:"expectedTable"`
		Rules         []string `json:"rules"`
	}
	if err := decodeStrictAgentJSON(raw, &req); err != nil {
		return fmt.Errorf("parse batch add request: %w", err)
	}
	if req.ExpectedTable != "" {
		if validateErr := nftgeneration.ValidateTableName(req.ExpectedTable); validateErr != nil {
			return fmt.Errorf("invalid expected nft table: %w", validateErr)
		}
	}
	if len(req.Rules) == 0 || len(req.Rules) > nftgeneration.MaxRuleBatchItems {
		return fmt.Errorf("batch add requires between 1 and %d rules", nftgeneration.MaxRuleBatchItems)
	}
	seen := make(map[string]struct{}, len(req.Rules))
	for _, rule := range req.Rules {
		if err := validateAddNftRule(rule); err != nil {
			return err
		}
		if _, duplicate := seen[rule]; duplicate {
			return errors.New("batch add contains duplicate canonical rule")
		}
		seen[rule] = struct{}{}
	}
	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	activeTable, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return err
	}
	if req.ExpectedTable != "" && activeTable != req.ExpectedTable {
		return fmt.Errorf("%s active table changed from %q to %q", nftgeneration.RetryableErrorPrefix, req.ExpectedTable, activeTable)
	}
	var transaction strings.Builder
	for _, rule := range req.Rules {
		rewritten, err := nftgeneration.RewriteCanonicalRule(rule, activeTable)
		if err != nil {
			return err
		}
		transaction.WriteString(rewritten)
		transaction.WriteByte('\n')
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.runStdin(commandCtx, "nft", []string{"-f", "-"}, transaction.String())
	if err != nil {
		return fmt.Errorf("batch add rules failed: %v, output: %s", err, string(output))
	}
	return nil
}

func executeAddNftRule(ctx context.Context, raw json.RawMessage, table string, run nftStdinRunner) error {
	rule, err := parseValidatedAddNftRule(raw)
	if err != nil {
		return err
	}
	return executeValidatedAddNftRule(ctx, rule, table, run)
}

func parseValidatedAddNftRule(raw json.RawMessage) (string, error) {
	var req struct {
		Chain string `json:"chain"`
		Rule  string `json:"rule"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", fmt.Errorf("parse request: %v", err)
	}
	if req.Chain != "" {
		return "", fmt.Errorf("separate nft chain is not allowed")
	}
	rule := req.Rule
	if err := validateAddNftRule(rule); err != nil {
		return "", err
	}
	return rule, nil
}

func executeValidatedAddNftRule(ctx context.Context, rule, table string, run nftStdinRunner) error {
	rewritten, err := nftgeneration.RewriteCanonicalRule(rule, table)
	if err != nil {
		return fmt.Errorf("rewrite nft rule for active table: %w", err)
	}
	if run == nil {
		return errors.New("add nft rule: missing nft runner")
	}
	output, err := run(ctx, "nft", []string{"-f", "-"}, rewritten+"\n")
	if err != nil {
		return fmt.Errorf("add rule failed: %v, output: %s", err, string(output))
	}

	return nil
}

var (
	nftRuleBaseCommentPattern      = regexp.MustCompile(`^"fp:[0-9]+:[0-9]+:[0-9]+"$`)
	nftRuleDirectionCommentPattern = regexp.MustCompile(`^"fp:[0-9]+:[0-9]+:[0-9]+:(?:up|down)"$`)
)

func validateAddNftRule(rule string) error {
	if strings.TrimSpace(rule) != rule || rule == "" {
		return fmt.Errorf("nft rule has surrounding whitespace")
	}
	if strings.IndexFunc(rule, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) && r != ' ' || r == ';' || r == '#'
	}) >= 0 {
		return fmt.Errorf("nft rule contains unsafe characters")
	}

	fields := strings.Split(rule, " ")
	for _, field := range fields {
		if field == "" {
			return fmt.Errorf("nft rule must use single ASCII spaces")
		}
	}
	if len(fields) < 14 || fields[0] != "add" || fields[1] != "rule" ||
		fields[2] != "inet" || fields[3] != "flux_panel" {
		return fmt.Errorf("unexpected nft rule prefix")
	}
	chain := fields[4]
	expression := fields[5:]
	switch chain {
	case "prerouting":
		return validateNftPreroutingExpression(expression)
	case "forward":
		return validateNftForwardExpression(expression)
	case "postrouting":
		return validateNftPostroutingExpression(expression)
	default:
		return fmt.Errorf("unexpected nft chain")
	}
}

func validateNftRuleHeader(fields []string) (string, error) {
	if len(fields) < 4 || fields[0] != "meta" || fields[1] != "nfproto" {
		return "", fmt.Errorf("unexpected nft rule expression")
	}
	if fields[2] != "ipv4" && fields[2] != "ipv6" {
		return "", fmt.Errorf("unexpected nft address family")
	}
	if fields[3] != "tcp" && fields[3] != "udp" {
		return "", fmt.Errorf("unexpected nft protocol")
	}
	return fields[2], nil
}

func validateNftPreroutingExpression(fields []string) error {
	family, err := validateNftRuleHeader(fields)
	if err != nil {
		return err
	}
	if len(fields) != 9 && len(fields) != 11 {
		return fmt.Errorf("unexpected nft prerouting rule")
	}
	if fields[4] != "dport" || !validNftPort(fields[5]) || fields[6] != "dnat" || fields[7] != "to" {
		return fmt.Errorf("unexpected nft prerouting rule")
	}
	target, err := netip.ParseAddrPort(fields[8])
	if err != nil || target.String() != fields[8] || target.Port() == 0 || target.Addr().Zone() != "" || !nftFamilyMatches(family, target.Addr()) {
		return fmt.Errorf("invalid nft dnat target")
	}
	return validateOptionalNftBaseComment(fields[9:])
}

func validateNftForwardExpression(fields []string) error {
	family, err := validateNftRuleHeader(fields)
	if err != nil {
		return err
	}
	if len(fields) < 17 || fields[4] != "dport" && fields[4] != "sport" || !validNftPort(fields[5]) {
		return fmt.Errorf("unexpected nft forward rule")
	}
	wantAddressType, wantAddressField, wantState := "ip", "daddr", "new,established,related"
	if family == "ipv6" {
		wantAddressType = "ip6"
	}
	if fields[4] == "sport" {
		wantAddressField = "saddr"
		wantState = "established,related"
	}
	// forward 规则必须绑定 conntrack 原始目的端口（公网入口端口）。仅匹配
	// DNAT 后目标地址/端口会让相同目标的多个转发命中第一条 accept 规则。
	if fields[6] != wantAddressType || fields[7] != wantAddressField ||
		fields[9] != "ct" || fields[10] != "original" || fields[11] != "proto-dst" || !validNftPort(fields[12]) ||
		fields[13] != "ct" || fields[14] != "state" || fields[15] != wantState {
		return fmt.Errorf("unexpected nft forward rule")
	}
	addr, err := netip.ParseAddr(fields[8])
	if err != nil || addr.String() != fields[8] || addr.Zone() != "" || !nftFamilyMatches(family, addr) {
		return fmt.Errorf("invalid nft forward address")
	}
	tail := fields[16:]
	direction := "up"
	if fields[4] == "sport" {
		direction = "down"
	}
	return validateNftForwardTail(tail, direction)
}

func validateNftPostroutingExpression(fields []string) error {
	family, err := validateNftRuleHeader(fields)
	if err != nil {
		return err
	}
	if len(fields) != 10 && len(fields) != 12 {
		return fmt.Errorf("unexpected nft postrouting rule")
	}
	wantAddressType := "ip"
	if family == "ipv6" {
		wantAddressType = "ip6"
	}
	if fields[4] != "dport" || !validNftPort(fields[5]) || fields[6] != wantAddressType || fields[7] != "daddr" || fields[9] != "masquerade" {
		return fmt.Errorf("unexpected nft postrouting rule")
	}
	addr, err := netip.ParseAddr(fields[8])
	if err != nil || addr.String() != fields[8] || addr.Zone() != "" || !nftFamilyMatches(family, addr) {
		return fmt.Errorf("invalid nft postrouting address")
	}
	return validateOptionalNftBaseComment(fields[10:])
}

func validateNftForwardTail(fields []string, direction string) error {
	if len(fields) == 1 && fields[0] == "accept" {
		return nil
	}
	if len(fields) == 3 && fields[0] == "accept" && fields[1] == "comment" && nftRuleBaseCommentPattern.MatchString(fields[2]) {
		return nil
	}
	if len(fields) == 4 && fields[0] == "counter" && fields[1] == "accept" && fields[2] == "comment" &&
		nftRuleDirectionCommentPattern.MatchString(fields[3]) && strings.HasSuffix(fields[3], ":"+direction+`"`) {
		return nil
	}
	return fmt.Errorf("unexpected nft forward verdict or comment")
}

func validateOptionalNftBaseComment(fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	if len(fields) != 2 || fields[0] != "comment" || !nftRuleBaseCommentPattern.MatchString(fields[1]) {
		return fmt.Errorf("unexpected nft rule comment")
	}
	return nil
}

func validNftPort(raw string) bool {
	port, err := strconv.Atoi(raw)
	return err == nil && port >= 1 && port <= 65535 && strconv.Itoa(port) == raw
}

func nftFamilyMatches(family string, addr netip.Addr) bool {
	if family == "ipv6" {
		return addr.Is6()
	}
	return addr.Is4()
}

// deleteNftRule 删除单条 nft 规则
func deleteNftRule(raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return deleteNftRuleWithOps(ctx, raw, defaultNftAgentOps())
}

func deleteNftRuleWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (err error) {
	var req struct {
		Chain  string `json:"chain"`
		Handle int    `json:"handle"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("parse request: %v", err)
	}
	if !validNftChain(req.Chain) {
		return errors.New("invalid nft chain")
	}
	if req.Handle <= 0 {
		return errors.New("invalid nft rule handle")
	}
	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	table, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.run(commandCtx, "nft", "delete", "rule", "inet", table, req.Chain, "handle", strconv.Itoa(req.Handle))
	if err != nil {
		return fmt.Errorf("delete rule failed: %v, output: %s", err, string(output))
	}

	return nil
}

func deleteNftRules(raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return deleteNftRulesWithOps(ctx, raw, defaultNftAgentOps())
}

func deleteNftRulesWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (err error) {
	if len(raw) == 0 || len(raw) > nftgeneration.MaxRuleBatchBytes {
		return fmt.Errorf("batch delete request must be between 1 and %d bytes", nftgeneration.MaxRuleBatchBytes)
	}
	var req struct {
		ExpectedTable string       `json:"expectedTable"`
		Handles       []RuleHandle `json:"handles"`
	}
	if err := decodeStrictAgentJSON(raw, &req); err != nil {
		return fmt.Errorf("parse batch delete request: %w", err)
	}
	if err := nftgeneration.ValidateTableName(req.ExpectedTable); err != nil {
		return fmt.Errorf("invalid expected nft table: %w", err)
	}
	if len(req.Handles) == 0 || len(req.Handles) > nftgeneration.MaxRuleBatchItems {
		return fmt.Errorf("batch delete requires between 1 and %d handles", nftgeneration.MaxRuleBatchItems)
	}
	seen := make(map[string]struct{}, len(req.Handles))
	for _, handle := range req.Handles {
		if !validNftChain(handle.Chain) || handle.Handle <= 0 {
			return errors.New("batch delete contains invalid chain or handle")
		}
		key := handle.Chain + ":" + strconv.Itoa(handle.Handle)
		if _, duplicate := seen[key]; duplicate {
			return errors.New("batch delete contains duplicate chain and handle")
		}
		seen[key] = struct{}{}
	}

	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	activeTable, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return err
	}
	if activeTable != req.ExpectedTable {
		return fmt.Errorf("%s active table changed from %q to %q", nftgeneration.RetryableErrorPrefix, req.ExpectedTable, activeTable)
	}
	var transaction strings.Builder
	transaction.Grow(len(req.Handles) * 96)
	for _, handle := range req.Handles {
		fmt.Fprintf(&transaction, "delete rule inet %s %s handle %d\n", activeTable, handle.Chain, handle.Handle)
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.runStdin(commandCtx, "nft", []string{"-f", "-"}, transaction.String())
	if err != nil {
		return fmt.Errorf("batch delete rules failed: %v, output: %s", err, string(output))
	}
	return nil
}

func replaceNftRules(raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return replaceNftRulesWithOps(ctx, raw, defaultNftAgentOps())
}

func replaceNftRulesWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (err error) {
	if len(raw) == 0 || len(raw) > nftgeneration.MaxRuleBatchBytes {
		return fmt.Errorf("replace request must be between 1 and %d bytes", nftgeneration.MaxRuleBatchBytes)
	}
	var req struct {
		ExpectedTable string       `json:"expectedTable"`
		DeleteHandles []RuleHandle `json:"deleteHandles"`
		AddRules      []string     `json:"addRules"`
	}
	if err := decodeStrictAgentJSON(raw, &req); err != nil {
		return fmt.Errorf("parse replace request: %w", err)
	}
	if err := nftgeneration.ValidateTableName(req.ExpectedTable); err != nil {
		return fmt.Errorf("invalid expected nft table: %w", err)
	}
	deleteCount := len(req.DeleteHandles)
	addCount := len(req.AddRules)
	if deleteCount > nftgeneration.MaxRuleBatchItems || addCount > nftgeneration.MaxRuleBatchItems ||
		deleteCount+addCount == 0 || deleteCount+addCount > nftgeneration.MaxRuleBatchItems {
		return fmt.Errorf("replace requires between 1 and %d combined handles and rules", nftgeneration.MaxRuleBatchItems)
	}
	seenHandles := make(map[string]struct{}, deleteCount)
	for _, handle := range req.DeleteHandles {
		if !validNftChain(handle.Chain) || handle.Handle <= 0 {
			return errors.New("replace contains invalid delete chain or handle")
		}
		key := handle.Chain + ":" + strconv.Itoa(handle.Handle)
		if _, duplicate := seenHandles[key]; duplicate {
			return errors.New("replace contains duplicate delete chain and handle")
		}
		seenHandles[key] = struct{}{}
	}
	seenRules := make(map[string]struct{}, addCount)
	for _, rule := range req.AddRules {
		if err := validateAddNftRule(rule); err != nil {
			return err
		}
		if _, duplicate := seenRules[rule]; duplicate {
			return errors.New("replace contains duplicate canonical add rule")
		}
		seenRules[rule] = struct{}{}
	}

	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	activeTable, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return err
	}
	if activeTable != req.ExpectedTable {
		return fmt.Errorf("%s active table changed from %q to %q", nftgeneration.RetryableErrorPrefix, req.ExpectedTable, activeTable)
	}
	var transaction strings.Builder
	transaction.Grow(len(raw))
	for _, handle := range req.DeleteHandles {
		fmt.Fprintf(&transaction, "delete rule inet %s %s handle %d\n", activeTable, handle.Chain, handle.Handle)
	}
	for _, rule := range req.AddRules {
		rewritten, rewriteErr := nftgeneration.RewriteCanonicalRule(rule, activeTable)
		if rewriteErr != nil {
			return rewriteErr
		}
		transaction.WriteString(rewritten)
		transaction.WriteByte('\n')
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.runStdin(commandCtx, "nft", []string{"-f", "-"}, transaction.String())
	if err != nil {
		return fmt.Errorf("replace rules failed: %v, output: %s", err, string(output))
	}
	return nil
}

// RuleHandle 规则的 handle 信息
type RuleHandle struct {
	Chain  string `json:"chain"`
	Handle int    `json:"handle"`
}

type ruleHandlesView struct {
	Table   string       `json:"table"`
	Handles []RuleHandle `json:"handles"`
}

// findRuleHandles 查找转发的规则 handle
func findRuleHandles(raw json.RawMessage) (ruleHandlesView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nftExecTimeout)
	defer cancel()
	return findRuleHandlesWithOps(ctx, raw, defaultNftAgentOps())
}

func findRuleHandlesWithOps(ctx context.Context, raw json.RawMessage, ops nftAgentOps) (view ruleHandlesView, err error) {
	var req struct {
		ForwardID int64 `json:"forwardId"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return ruleHandlesView{}, fmt.Errorf("parse request: %v", err)
	}
	if req.ForwardID <= 0 {
		return ruleHandlesView{}, errors.New("invalid forward ID")
	}

	release, err := acquireNftAgentLock(ops)
	if err != nil {
		return ruleHandlesView{}, err
	}
	defer func() { err = errors.Join(err, releaseNftAgentLock(release)) }()
	table, err := resolveActiveNftTableWithReader(ctx, ops.activeMarkerPath, ops.readMarker, ops.run)
	if err != nil {
		return ruleHandlesView{}, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, nftExecTimeout)
	defer cancel()
	output, err := ops.run(commandCtx, "nft", "-a", "list", "table", "inet", table)
	if err != nil {
		return ruleHandlesView{}, fmt.Errorf("list rules failed: %v, output: %s", err, string(output))
	}
	handles, err := parseRuleHandles(output, table, req.ForwardID)
	if err != nil {
		return ruleHandlesView{}, err
	}
	return ruleHandlesView{Table: table, Handles: handles}, nil
}

func parseRuleHandles(output []byte, table string, forwardID int64) ([]RuleHandle, error) {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return nil, err
	}
	// 解析输出，查找包含 "fp:<forwardId>:" 的规则
	commentPrefix := fmt.Sprintf("fp:%d:", forwardID)
	currentChain := ""
	tableSeen := false
	handles := make([]RuleHandle, 0)
	seenHandles := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), maxNftOutputLineBytes)
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxNftOutputLines {
			return nil, fmt.Errorf("nft handle output exceeds %d lines", maxNftOutputLines)
		}
		trimmed := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(trimmed, "table ") {
			matched, headerErr := parseNftObjectHeader(trimmed, "table inet "+table+" {")
			if headerErr != nil || !matched || tableSeen {
				return nil, errors.New("nft handle output table identity mismatch")
			}
			tableSeen = true
			continue
		}

		// 检测链
		if strings.HasPrefix(trimmed, "chain ") {
			if !tableSeen {
				return nil, errors.New("nft handle output chain precedes active table")
			}
			fields := strings.Fields(trimmed)
			if len(fields) < 2 || !validNftChain(fields[1]) {
				return nil, errors.New("nft handle output has invalid chain")
			}
			matched, headerErr := parseNftObjectHeader(trimmed, "chain "+fields[1]+" {")
			if headerErr != nil || !matched {
				return nil, errors.New("nft handle output has malformed chain header")
			}
			currentChain = fields[1]
			continue
		}
		if trimmed == "}" || strings.HasPrefix(trimmed, "type ") && strings.HasSuffix(trimmed, ";") {
			continue
		}
		if !tableSeen || !validNftChain(currentChain) {
			return nil, errors.New("nft handle output rule precedes a validated chain")
		}
		handle, handleErr := parseNftRuleOutputHandle(trimmed)
		if handleErr != nil {
			return nil, handleErr
		}
		key := currentChain + ":" + strconv.Itoa(handle)
		if _, duplicate := seenHandles[key]; duplicate {
			return nil, errors.New("nft rule output has duplicate handle")
		}
		seenHandles[key] = struct{}{}

		// 查找包含注释的规则
		if strings.Contains(trimmed, commentPrefix) {
			handles = append(handles, RuleHandle{Chain: currentChain, Handle: handle})
			if len(handles) > maxNftRuleResults {
				return nil, fmt.Errorf("nft handle output exceeds %d results", maxNftRuleResults)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan nft handle output: %w", err)
	}
	if !tableSeen {
		return nil, errors.New("nft handle output is missing the active table")
	}

	return handles, nil
}

func parseNftObjectHeader(line, base string) (bool, error) {
	if line == base {
		return true, nil
	}
	prefix := base + " # handle "
	if !strings.HasPrefix(line, prefix) {
		return false, nil
	}
	if _, err := parseCanonicalPositiveInt(line[len(prefix):]); err != nil {
		return false, errors.New("invalid nft object handle")
	}
	return true, nil
}

func parseNftRuleOutputHandle(line string) (int, error) {
	const marker = " # handle "
	if strings.Count(line, marker) != 1 {
		return 0, errors.New("nft rule has missing or ambiguous handle")
	}
	_, raw, _ := strings.Cut(line, marker)
	handle, err := parseCanonicalPositiveInt(raw)
	if err != nil {
		return 0, errors.New("nft rule has invalid handle")
	}
	return handle, nil
}

func parseCanonicalPositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || strconv.Itoa(value) != raw {
		return 0, errors.New("invalid positive integer")
	}
	return value, nil
}

func validNftChain(chain string) bool {
	switch chain {
	case "prerouting", "forward", "postrouting":
		return true
	default:
		return false
	}
}
