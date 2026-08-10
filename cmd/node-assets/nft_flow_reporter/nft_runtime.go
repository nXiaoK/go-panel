package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

const (
	defaultNftCommandTimeout     = 2 * time.Second
	defaultNftOutputLimit        = 256 << 10
	defaultNftCounterOutputLimit = 32 << 20
	defaultNftRuntimeDir         = "/run/flux-nftables"
	maxNftJSONDepth              = 32
	maxProbeNameAttempts         = 16
	debian11NftVersion           = "0.9.8"
	debian11NftReleaseName       = "E.D.S."
	debian12NftVersion           = "1.0.6"
	debian12NftReleaseName       = "Lester Gooch #5"
)

type nftCommandFunc func(context.Context, string, []string, io.Writer, io.Writer) error

// execNftRuntime invokes nft directly. Transactions are durable, closed 0600
// files before nft sees them; command output is captured by bounded writers.
type execNftRuntime struct {
	nftPath               string
	tempDir               string
	timeout               time.Duration
	maxOutputBytes        int
	maxCounterOutputBytes int
	random                io.Reader
	command               nftCommandFunc
}

func newExecNftRuntime() *execNftRuntime {
	return &execNftRuntime{
		nftPath:               "nft",
		tempDir:               defaultNftRuntimeDir,
		timeout:               defaultNftCommandTimeout,
		maxOutputBytes:        defaultNftOutputLimit,
		maxCounterOutputBytes: defaultNftCounterOutputLimit,
		random:                rand.Reader,
		command:               runNftCommand,
	}
}

func runNftCommand(ctx context.Context, path string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (r *execNftRuntime) Probe(ctx context.Context) error {
	before, err := r.inspectTables(ctx)
	if err != nil {
		return fmt.Errorf("inspect nft tables before capability probe: %w", err)
	}
	if err := validateDiscoveredTables(before); err != nil {
		return fmt.Errorf("unsafe nft state before capability probe: %w", err)
	}
	probeName, err := r.uniqueProbeName(before)
	if err != nil {
		return err
	}

	fail := func(operationErr error) error {
		cleanupErr := r.cleanupProbe(probeName, before)
		if cleanupErr != nil {
			return errors.Join(operationErr, fmt.Errorf("capability probe cleanup: %w", cleanupErr))
		}
		return operationErr
	}
	create := "create table inet " + probeName + " { flags dormant; }\n" +
		"add chain inet " + probeName + " forward { type filter hook forward priority filter; policy accept; }\n"
	if err := r.executeTransaction(ctx, create); err != nil {
		return fail(fmt.Errorf("create dormant capability probe: %w", err))
	}
	if err := r.verifyProbeState(ctx, before, probeName, true, true); err != nil {
		return fail(fmt.Errorf("verify dormant capability probe: %w", err))
	}
	if err := r.executeTransaction(ctx, activeTableTransaction(probeName)); err != nil {
		return fail(fmt.Errorf("activate capability probe: %w", err))
	}
	if err := r.verifyProbeState(ctx, before, probeName, false, true); err != nil {
		return fail(fmt.Errorf("verify active capability probe: %w", err))
	}
	if err := r.executeTransaction(ctx, dormantTableTransaction(probeName)); err != nil {
		return fail(fmt.Errorf("deactivate capability probe: %w", err))
	}
	if err := r.verifyProbeState(ctx, before, probeName, true, true); err != nil {
		return fail(fmt.Errorf("verify deactivated capability probe: %w", err))
	}
	if err := r.Delete(ctx, probeName); err != nil {
		return fail(fmt.Errorf("delete capability probe: %w", err))
	}
	if err := r.verifyProbeState(ctx, before, probeName, false, false); err != nil {
		return fail(fmt.Errorf("verify deleted capability probe: %w", err))
	}
	return nil
}

func (r *execNftRuntime) Discover(ctx context.Context) ([]GenerationTable, error) {
	tables, err := r.inspectTables(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateDiscoveredTables(tables); err != nil {
		return nil, err
	}
	return tables, nil
}

func (r *execNftRuntime) Stage(ctx context.Context, table string, rules []string) error {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return fmt.Errorf("stage nft generation: %w", err)
	}
	if table == nftgeneration.LegacyTable {
		return errors.New("stage nft generation: legacy table is not a new generation")
	}
	rewritten := make([]string, len(rules))
	for i, rule := range rules {
		line, err := nftgeneration.RewriteCanonicalRule(rule, table)
		if err != nil {
			return fmt.Errorf("stage nft generation rule %d: %w", i, err)
		}
		rewritten[i] = line
	}

	var transaction strings.Builder
	fmt.Fprintf(&transaction, "create table inet %s { flags dormant; }\n", table)
	fmt.Fprintf(&transaction, "add chain inet %s prerouting { type nat hook prerouting priority dstnat; policy accept; }\n", table)
	fmt.Fprintf(&transaction, "add chain inet %s postrouting { type nat hook postrouting priority srcnat; policy accept; }\n", table)
	fmt.Fprintf(&transaction, "add chain inet %s forward { type filter hook forward priority filter; policy accept; }\n", table)
	for _, rule := range rewritten {
		transaction.WriteString(rule)
		transaction.WriteByte('\n')
	}
	if err := r.executeTransaction(ctx, transaction.String()); err != nil {
		return fmt.Errorf("stage dormant nft generation: %w", err)
	}
	return nil
}

func (r *execNftRuntime) Switch(ctx context.Context, oldTable, newTable string) error {
	if err := nftgeneration.ValidateTableName(oldTable); err != nil {
		return fmt.Errorf("switch old nft generation: %w", err)
	}
	if err := nftgeneration.ValidateTableName(newTable); err != nil {
		return fmt.Errorf("switch new nft generation: %w", err)
	}
	if oldTable == newTable {
		return errors.New("switch nft generation: old and new table are identical")
	}
	transaction := dormantTableTransaction(oldTable) + activeTableTransaction(newTable)
	if err := r.executeTransaction(ctx, transaction); err != nil {
		return fmt.Errorf("atomically switch nft generations: %w", err)
	}
	return nil
}

func (r *execNftRuntime) ReadCounters(ctx context.Context, table string) (map[flowKey]counters, error) {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return nil, fmt.Errorf("read nft counters: %w", err)
	}
	stdout, _, err := r.runWithOutputLimits(
		ctx,
		[]string{"list", "table", "inet", table},
		r.counterOutputLimit(),
		r.outputLimit(),
	)
	if err != nil {
		return nil, fmt.Errorf("read nft counters from %s: %w", table, err)
	}
	return collectCounters(stdout)
}

func (r *execNftRuntime) Delete(ctx context.Context, table string) error {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return fmt.Errorf("delete nft generation: %w", err)
	}
	if err := r.executeTransaction(ctx, "delete table inet "+table+"\n"); err != nil {
		return fmt.Errorf("delete nft generation %s: %w", table, err)
	}
	return nil
}

func dormantTableTransaction(table string) string {
	return "add table inet " + table + " { flags dormant; }\n"
}

// nft updates an existing table with a plain add command. Its parser rejects
// an empty flags block, so activation must not synthesize one.
func activeTableTransaction(table string) string {
	return "add table inet " + table + "\n"
}

func (r *execNftRuntime) executeTransaction(ctx context.Context, transaction string) error {
	if transaction == "" {
		return errors.New("empty nft transaction")
	}
	dir := r.tempDir
	if dir == "" {
		dir = defaultNftRuntimeDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create nft transaction directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".nft-transaction-*")
	if err != nil {
		return fmt.Errorf("create nft transaction: %w", err)
	}
	path := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(path)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure nft transaction: %w", err)
	}
	if _, err := io.WriteString(tmp, transaction); err != nil {
		return fmt.Errorf("write nft transaction: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync nft transaction: %w", err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close nft transaction: %w", err)
	}
	closed = true
	if _, _, err := r.run(ctx, []string{"-f", path}); err != nil {
		return err
	}
	return nil
}

func (r *execNftRuntime) run(ctx context.Context, args []string) (string, string, error) {
	limit := r.outputLimit()
	return r.runWithOutputLimits(ctx, args, limit, limit)
}

func (r *execNftRuntime) outputLimit() int {
	if r.maxOutputBytes > 0 {
		return r.maxOutputBytes
	}
	return defaultNftOutputLimit
}

func (r *execNftRuntime) counterOutputLimit() int {
	if r.maxCounterOutputBytes > 0 {
		return r.maxCounterOutputBytes
	}
	return defaultNftCounterOutputLimit
}

func (r *execNftRuntime) runWithOutputLimits(ctx context.Context, args []string, stdoutLimit, stderrLimit int) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultNftCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout := newCappedWriter(stdoutLimit)
	stderr := newCappedWriter(stderrLimit)
	command := r.command
	if command == nil {
		command = runNftCommand
	}
	path := r.nftPath
	if path == "" {
		path = "nft"
	}
	err := command(ctx, path, append([]string(nil), args...), stdout, stderr)
	if stdout.overflow {
		return stdout.String(), stderr.String(), fmt.Errorf("nft command stdout output limit exceeded (%d bytes)", stdoutLimit)
	}
	if stderr.overflow {
		return stdout.String(), stderr.String(), fmt.Errorf("nft command stderr output limit exceeded (%d bytes)", stderrLimit)
	}
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("nft command deadline: %w%s", ctx.Err(), boundedCommandDiagnostic(stdout.String(), stderr.String()))
	}
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("nft command failed: %w%s", err, boundedCommandDiagnostic(stdout.String(), stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

func boundedCommandDiagnostic(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout == "" && stderr == "" {
		return ""
	}
	return fmt.Sprintf(" (stdout=%q stderr=%q)", stdout, stderr)
}

type cappedWriter struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newCappedWriter(limit int) *cappedWriter { return &cappedWriter{limit: limit} }

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:min(len(p), remaining)])
	}
	if len(p) > remaining {
		w.overflow = true
	}
	// Report the complete input consumed so os/exec does not turn intentional
	// truncation into an unrelated short-write error.
	return len(p), nil
}

func (w *cappedWriter) String() string { return w.buffer.String() }

func (r *execNftRuntime) inspectTables(ctx context.Context) ([]GenerationTable, error) {
	stdout, _, err := r.run(ctx, []string{"-j", "list", "tables"})
	if err != nil {
		return nil, fmt.Errorf("list nft tables: %w", err)
	}
	tables, parseErr := parseNftTables([]byte(stdout))
	if parseErr == nil {
		return tables, nil
	}
	var flagErr *unsupportedNftTableFlagError
	if !errors.As(parseErr, &flagErr) {
		return nil, parseErr
	}

	// Debian 11/12 随附的旧版 nftables 偶尔会把 dormant 的 JSON flag 串位成
	// 发行代号、另一张表名或其他无关字符串。这里只对白名单中的精确版本启用
	// 兼容，且 JSON 其余结构仍须严格通过；随后用对应表的文本头二次确认，
	// 任何歧义继续失败关闭。
	candidates, candidateErr := parseNftTablesWithFlagClassifier([]byte(stdout), func(_ []string, metainfo *nftMetainfo) (bool, error) {
		if !hasKnownNftReleaseNameFlagQuirk(metainfo) {
			return false, errors.New("nft inventory is not an exact supported flag compatibility target")
		}
		return false, nil
	})
	if candidateErr != nil {
		return nil, errors.Join(parseErr, candidateErr)
	}
	verified, verifyErr := r.verifyKnownQuirkTableStates(ctx, candidates)
	if verifyErr != nil {
		return nil, errors.Join(parseErr, verifyErr)
	}
	return verified, nil
}

func (r *execNftRuntime) verifyKnownQuirkTableStates(ctx context.Context, tables []GenerationTable) ([]GenerationTable, error) {
	verified := append([]GenerationTable(nil), tables...)
	for i := range verified {
		stdout, _, err := r.runWithOutputLimits(
			ctx,
			[]string{"list", "table", "inet", verified[i].Name},
			r.counterOutputLimit(),
			r.outputLimit(),
		)
		if err != nil {
			return nil, fmt.Errorf("verify compatible nft table %q text state: %w", verified[i].Name, err)
		}
		dormant, err := parseNftTableTextState(stdout, verified[i].Name)
		if err != nil {
			return nil, fmt.Errorf("verify compatible nft table %q text state: %w", verified[i].Name, err)
		}
		verified[i].Dormant = dormant
	}
	return verified, nil
}

func parseNftTableTextState(raw, table string) (bool, error) {
	if err := nftgeneration.ValidateTableName(table); err != nil {
		return false, err
	}
	lines := strings.Split(raw, "\n")
	headerIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			headerIndex = i
			break
		}
	}
	if headerIndex < 0 {
		return false, errors.New("empty nft table text")
	}
	header := strings.Fields(lines[headerIndex])
	if len(header) != 4 || header[0] != "table" || header[1] != "inet" || header[2] != table || header[3] != "{" {
		return false, errors.New("nft table text has an unexpected header")
	}

	dormant := false
	for _, line := range lines[headerIndex+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch trimmed {
		case "flags dormant", "flags dormant;":
			if dormant {
				return false, errors.New("nft table text has duplicate dormant flags")
			}
			dormant = true
			continue
		}
		if strings.HasPrefix(trimmed, "chain ") && strings.HasSuffix(trimmed, " {") {
			return dormant, nil
		}
		return false, fmt.Errorf("nft table text has an unexpected top-level statement %q", trimmed)
	}
	return false, errors.New("nft table text has no chain declaration")
}

type nftListDocument struct {
	Nftables *[]json.RawMessage `json:"nftables"`
}

type nftListElement struct {
	Metainfo json.RawMessage `json:"metainfo,omitempty"`
	Table    json.RawMessage `json:"table,omitempty"`
}

type nftMetainfo struct {
	Version           string `json:"version"`
	ReleaseName       string `json:"release_name"`
	JSONSchemaVersion int    `json:"json_schema_version"`
}

type nftTableInfo struct {
	Family  string          `json:"family"`
	Name    string          `json:"name"`
	Handle  json.RawMessage `json:"handle,omitempty"`
	Flags   json.RawMessage `json:"flags,omitempty"`
	Comment json.RawMessage `json:"comment,omitempty"`
}

type nftTableCandidate struct {
	index    int
	rawTable json.RawMessage
}

type nftTableFlagClassifier func([]string, *nftMetainfo) (bool, error)

func parseNftTables(raw []byte) ([]GenerationTable, error) {
	return parseNftTablesWithFlagClassifier(raw, classifyNftTableFlags)
}

func parseNftTablesWithFlagClassifier(raw []byte, classifyFlags nftTableFlagClassifier) ([]GenerationTable, error) {
	if classifyFlags == nil {
		return nil, errors.New("missing nft table flag classifier")
	}
	if err := validateUniqueJSON(raw); err != nil {
		return nil, fmt.Errorf("decode nft table inventory: %w", err)
	}
	var document nftListDocument
	if err := decodeStrictJSON(raw, &document); err != nil {
		return nil, fmt.Errorf("decode nft table inventory: %w", err)
	}
	if document.Nftables == nil {
		return nil, errors.New("decode nft table inventory: missing or null nftables array")
	}
	candidates := make([]nftTableCandidate, 0)
	var metainfo *nftMetainfo
	for i, rawElement := range *document.Nftables {
		var element nftListElement
		if err := decodeStrictJSON(rawElement, &element); err != nil {
			return nil, fmt.Errorf("decode nft table inventory element %d: %w", i, err)
		}
		hasMetainfo := len(element.Metainfo) != 0
		hasTable := len(element.Table) != 0
		if hasMetainfo == hasTable {
			return nil, fmt.Errorf("ambiguous nft table inventory element %d", i)
		}
		if hasMetainfo {
			if metainfo != nil {
				return nil, errors.New("duplicate nft inventory metainfo")
			}
			var metadata nftMetainfo
			if err := decodeStrictJSON(element.Metainfo, &metadata); err != nil {
				return nil, fmt.Errorf("decode nft inventory metainfo: %w", err)
			}
			if metadata.Version == "" || metadata.ReleaseName == "" || metadata.JSONSchemaVersion <= 0 {
				return nil, errors.New("invalid nft inventory metainfo")
			}
			metainfo = &metadata
			continue
		}
		candidates = append(candidates, nftTableCandidate{index: i, rawTable: element.Table})
	}

	tables := make([]GenerationTable, 0)
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		i := candidate.index
		var table nftTableInfo
		if err := decodeStrictJSON(candidate.rawTable, &table); err != nil {
			return nil, fmt.Errorf("decode nft table at inventory element %d: %w", i, err)
		}
		if table.Name == "" || !validNftFamily(table.Family) {
			return nil, fmt.Errorf("invalid nft table identity at inventory element %d", i)
		}
		if len(table.Handle) > 0 {
			var number json.Number
			if err := json.Unmarshal(table.Handle, &number); err != nil {
				return nil, fmt.Errorf("invalid nft table handle at inventory element %d", i)
			}
			handle, err := strconv.ParseUint(number.String(), 10, 64)
			if err != nil || handle == 0 {
				return nil, fmt.Errorf("invalid nft table handle at inventory element %d", i)
			}
		}
		if len(table.Comment) > 0 {
			if bytes.Equal(bytes.TrimSpace(table.Comment), []byte("null")) {
				return nil, fmt.Errorf("invalid nft table comment at inventory element %d", i)
			}
			var comment string
			if err := json.Unmarshal(table.Comment, &comment); err != nil {
				return nil, fmt.Errorf("invalid nft table comment at inventory element %d", i)
			}
		}
		flags, err := decodeNftTableFlags(table.Flags)
		if err != nil {
			return nil, fmt.Errorf("invalid nft table flags at inventory element %d: %w", i, err)
		}
		owned := table.Name == nftgeneration.LegacyTable || strings.HasPrefix(table.Name, nftgeneration.LegacyTable+"_g_")
		if !owned {
			continue
		}
		if table.Family != "inet" {
			return nil, fmt.Errorf("Flux nft table %q has unexpected family %q", table.Name, table.Family)
		}
		if err := nftgeneration.ValidateTableName(table.Name); err != nil {
			return nil, fmt.Errorf("ambiguous Flux nft table ownership: %w", err)
		}
		if _, duplicate := seen[table.Name]; duplicate {
			return nil, fmt.Errorf("duplicate Flux nft table %q", table.Name)
		}
		seen[table.Name] = struct{}{}
		dormant, err := classifyFlags(flags, metainfo)
		if err != nil {
			return nil, fmt.Errorf("Flux nft table %q: %w", table.Name, err)
		}
		tables = append(tables, GenerationTable{Name: table.Name, Dormant: dormant})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	return tables, nil
}

func classifyNftTableFlags(flags []string, metainfo *nftMetainfo) (bool, error) {
	seen := make(map[string]struct{}, len(flags))
	dormant := false
	releaseNameQuirk := false
	for _, flag := range flags {
		if _, duplicate := seen[flag]; duplicate {
			return false, fmt.Errorf("duplicate flag %q", flag)
		}
		seen[flag] = struct{}{}
		switch {
		case flag == "dormant":
			dormant = true
		case isKnownNftReleaseNameFlag(flag, metainfo):
			releaseNameQuirk = true
		default:
			return false, &unsupportedNftTableFlagError{flag: flag}
		}
	}
	if releaseNameQuirk {
		// 受影响的旧版 nftables 会把 dormant 的唯一 flag 错误输出为发行代号；
		// 精确 metainfo 匹配保证该兼容不会把其他版本的未知 flag 当作 dormant。
		return true, nil
	}
	return dormant, nil
}

type unsupportedNftTableFlagError struct {
	flag string
}

func (e *unsupportedNftTableFlagError) Error() string {
	return fmt.Sprintf("unsupported flag %q", e.flag)
}

func hasKnownNftReleaseNameFlagQuirk(metainfo *nftMetainfo) bool {
	if metainfo == nil || metainfo.JSONSchemaVersion != 1 {
		return false
	}
	switch {
	case metainfo.Version == debian11NftVersion && metainfo.ReleaseName == debian11NftReleaseName:
		return true
	case metainfo.Version == debian12NftVersion && metainfo.ReleaseName == debian12NftReleaseName:
		return true
	default:
		return false
	}
}

func isKnownNftReleaseNameFlag(flag string, metainfo *nftMetainfo) bool {
	return hasKnownNftReleaseNameFlagQuirk(metainfo) && flag == metainfo.ReleaseName
}

func decodeNftTableFlags(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty flags")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("null flags")
	}
	switch trimmed[0] {
	case '"':
		var flag string
		if err := decodeStrictJSON(trimmed, &flag); err != nil {
			return nil, err
		}
		return []string{flag}, nil
	case '[':
		var flags []string
		if err := decodeStrictJSON(trimmed, &flags); err != nil {
			return nil, err
		}
		return flags, nil
	default:
		return nil, errors.New("flags must be a string or an array of strings")
	}
}

func validNftFamily(family string) bool {
	switch family {
	case "ip", "ip6", "inet", "arp", "bridge", "netdev":
		return true
	default:
		return false
	}
}

func decodeStrictJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON document")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateUniqueJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkUniqueJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	return nil
}

func walkUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxNftJSONDepth {
		return errors.New("nft JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("non-string nft JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate nft JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated nft JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated nft JSON array")
		}
	default:
		return errors.New("unexpected nft JSON delimiter")
	}
	return nil
}

func validateDiscoveredTables(tables []GenerationTable) error {
	active, dormant := 0, 0
	for _, table := range tables {
		if table.Dormant {
			dormant++
		} else {
			active++
		}
	}
	if active > 1 {
		return fmt.Errorf("ambiguous Flux nft state: %d active tables", active)
	}
	if dormant > 1 {
		return fmt.Errorf("ambiguous Flux nft state: %d dormant tables", dormant)
	}
	return nil
}

func (r *execNftRuntime) uniqueProbeName(existing []GenerationTable) (string, error) {
	used := make(map[string]struct{}, len(existing))
	for _, table := range existing {
		used[table.Name] = struct{}{}
	}
	for range maxProbeNameAttempts {
		name, err := nftgeneration.NewTableName(r.random)
		if err != nil {
			return "", fmt.Errorf("generate nft capability probe name: %w", err)
		}
		if err := nftgeneration.ValidateTableName(name); err != nil {
			return "", fmt.Errorf("validate nft capability probe name: %w", err)
		}
		if _, exists := used[name]; !exists {
			return name, nil
		}
	}
	return "", errors.New("generate unique nft capability probe name: collision limit reached")
}

func (r *execNftRuntime) verifyProbeState(ctx context.Context, before []GenerationTable, probeName string, dormant, present bool) error {
	after, err := r.inspectTables(ctx)
	if err != nil {
		return err
	}
	filtered := make([]GenerationTable, 0, len(after))
	found := false
	for _, table := range after {
		if table.Name == probeName {
			if found || !present || table.Dormant != dormant {
				return fmt.Errorf("unexpected capability probe state for %s", probeName)
			}
			found = true
			continue
		}
		filtered = append(filtered, table)
	}
	if found != present {
		return fmt.Errorf("capability probe presence=%v, want %v", found, present)
	}
	if !reflect.DeepEqual(filtered, before) {
		return fmt.Errorf("existing Flux nft tables changed during capability probe: before=%v after=%v", before, filtered)
	}
	return nil
}

func (r *execNftRuntime) cleanupProbe(probeName string, before []GenerationTable) error {
	deleteErr := r.Delete(context.Background(), probeName)
	verifyErr := r.verifyProbeState(context.Background(), before, probeName, false, false)
	if verifyErr == nil {
		// An nft delete can report failure after the kernel has completed it. A
		// machine-readable absence check resolves that ambiguity safely.
		return nil
	}
	return errors.Join(deleteErr, verifyErr)
}
