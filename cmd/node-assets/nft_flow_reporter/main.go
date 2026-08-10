package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

const (
	configPath        = "/etc/flux-nftables/config.env"
	statePath         = "/var/lib/flux-nftables/flow-state.json"
	maxRulesFileBytes = 32 << 20
	maxRules          = 100_000
	maxRuleLineBytes  = 16 << 10
)

var errReporterLocked = errors.New("nft flow reporter is already running")

var (
	commentRE = regexp.MustCompile(`fp:(-?[0-9]+):(-?[0-9]+):(-?[0-9]+):(up|down)`)
	bytesRE   = regexp.MustCompile(`bytes[[:space:]]+([0-9]+)`)
)

type flowKey struct {
	ForwardID    int64
	UserID       int64
	UserTunnelID int64
}

type counters struct {
	Up   int64
	Down int64
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--refresh" {
		if len(os.Args) != 5 && len(os.Args) != 6 {
			fmt.Fprintln(os.Stderr, "usage: nft_flow_reporter --refresh <rules-file> <server-addr> <secret> [table]")
			os.Exit(2)
		}
		currentTable := nftgeneration.LegacyTable
		if len(os.Args) == 6 {
			currentTable = os.Args[5]
		}
		unlock, err := acquireReporterLock(nftgeneration.LockPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer unlock()
		if err := runRefreshOnce(os.Args[2], os.Args[3], os.Args[4], currentTable); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	serverAddr, secret, table := configFromArgsAndEnv()
	if serverAddr == "" || secret == "" {
		fmt.Fprintln(os.Stderr, "usage: nft_flow_reporter <server-addr> <secret>")
		os.Exit(2)
	}
	unlock, err := acquireReporterLock(nftgeneration.LockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer unlock()

	if err := runReporterOnce(serverAddr, secret, table); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runReporterOnce(serverAddr, secret, table string) error {
	runtime := newExecNftRuntime()
	r := reporter{
		store:      fileJournalStore{path: statePath},
		runtime:    runtime,
		serverAddr: serverAddr,
		secret:     secret,
		readCounters: func(table string) (map[flowKey]counters, error) {
			return runtime.ReadCounters(context.Background(), table)
		},
		upload: upload,
	}
	return runReporterWith(context.Background(), r, table)
}

func runReporterWith(ctx context.Context, r reporter, configuredTable string) error {
	journal, err := r.store.load()
	if err != nil {
		return err
	}
	if err := r.recoverRefresh(ctx, &journal); err != nil {
		return err
	}
	// configuredTable is retained only for old invocation compatibility. Once
	// migrated, the reconciled durable active table is authoritative.
	_ = configuredTable
	return r.runOnce(r.serverAddr, r.secret, journal.ActiveTable)
}

func runRefreshOnce(rulesPath, serverAddr, secret, currentTable string) error {
	rules, err := readCanonicalRules(rulesPath)
	if err != nil {
		return err
	}
	runtime := newExecNftRuntime()
	r := reporter{
		store: fileJournalStore{path: statePath}, runtime: runtime,
		serverAddr: serverAddr, secret: secret, upload: upload,
	}
	return r.refreshRules(context.Background(), refreshRequest{
		Rules: rules, ServerAddr: serverAddr, Secret: secret, CurrentTable: currentTable,
	})
}

func readCanonicalRules(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open nft rules file: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, maxRuleLineBytes+1)
	rules := make([]string, 0, 1024)
	totalBytes := 0
	for {
		line, readErr := reader.ReadSlice('\n')
		totalBytes += len(line)
		if totalBytes > maxRulesFileBytes {
			return nil, errors.New("nft rules file exceeds size limit")
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("nft rule line length exceeds %d bytes", maxRuleLineBytes)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read nft rules file: %w", readErr)
		}
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if len(line) > maxRuleLineBytes {
			return nil, fmt.Errorf("nft rule line length exceeds %d bytes", maxRuleLineBytes)
		}
		if len(rules) == maxRules {
			return nil, fmt.Errorf("nft rule count exceeds %d", maxRules)
		}
		rule := string(line)
		if _, err := nftgeneration.RewriteCanonicalRule(rule, nftgeneration.LegacyTable); err != nil {
			return nil, fmt.Errorf("invalid canonical nft rule %d: %w", len(rules)+1, err)
		}
		rules = append(rules, rule)
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return rules, nil
}

func configFromArgsAndEnv() (serverAddr, secret, table string) {
	table = "flux_panel"
	env := readEnvFile(configPath)
	if len(os.Args) > 1 {
		serverAddr = os.Args[1]
	} else {
		serverAddr = env["SERVER_ADDR"]
	}
	if len(os.Args) > 2 {
		secret = os.Args[2]
	} else {
		secret = env["SECRET"]
	}
	if env["NFT_TABLE_NAME"] != "" {
		table = env["NFT_TABLE_NAME"]
	}
	return serverAddr, secret, table
}

func collectCounters(text string) (map[flowKey]counters, error) {
	stats := map[flowKey]*counters{}
	for lineNumber, line := range strings.Split(text, "\n") {
		comment := commentRE.FindStringSubmatch(line)
		if len(comment) != 5 {
			continue
		}
		bytesMatch := bytesRE.FindStringSubmatch(line)
		if len(bytesMatch) != 2 {
			return nil, fmt.Errorf("directional nft counter at line %d is missing bytes", lineNumber+1)
		}
		forwardID, err := parseCounterID(comment[1], false)
		if err != nil {
			return nil, fmt.Errorf("invalid forward id at nft counter line %d: %w", lineNumber+1, err)
		}
		userID, err := parseCounterID(comment[2], false)
		if err != nil {
			return nil, fmt.Errorf("invalid user id at nft counter line %d: %w", lineNumber+1, err)
		}
		userTunnelID, err := parseCounterID(comment[3], true)
		if err != nil {
			return nil, fmt.Errorf("invalid user tunnel id at nft counter line %d: %w", lineNumber+1, err)
		}
		byteCount, err := strconv.ParseInt(bytesMatch[1], 10, 64)
		if err != nil || byteCount < 0 {
			return nil, fmt.Errorf("invalid byte count at nft counter line %d", lineNumber+1)
		}
		key := flowKey{ForwardID: forwardID, UserID: userID, UserTunnelID: userTunnelID}
		if stats[key] == nil {
			stats[key] = &counters{}
		}
		if comment[4] == "up" {
			if stats[key].Up > math.MaxInt64-byteCount {
				return nil, fmt.Errorf("up byte counter overflow at nft counter line %d", lineNumber+1)
			}
			stats[key].Up += byteCount
		} else {
			if stats[key].Down > math.MaxInt64-byteCount {
				return nil, fmt.Errorf("down byte counter overflow at nft counter line %d", lineNumber+1)
			}
			stats[key].Down += byteCount
		}
	}

	out := make(map[flowKey]counters, len(stats))
	for key, counter := range stats {
		out[key] = *counter
	}
	return out, nil
}

func parseCounterID(raw string, allowZero bool) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if id < 0 {
		return 0, errors.New("identifier must not be negative")
	}
	if id == 0 && !allowZero {
		return 0, errors.New("identifier must be positive")
	}
	return id, nil
}

func upload(serverAddr, secret string, payload []byte) (dto.NftFlowAckDto, error) {
	endpoint := buildBaseURL(serverAddr) + "/flow/nft-upload-v2"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", secret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dto.NftFlowAckDto{}, fmt.Errorf("unexpected status: %s", resp.Status)
	}
	limited, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	if len(limited) > 64<<10 {
		return dto.NftFlowAckDto{}, errors.New("nft acknowledgement too large")
	}
	var ack dto.NftFlowAckDto
	decoder := json.NewDecoder(bytes.NewReader(limited))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ack); err != nil {
		return dto.NftFlowAckDto{}, fmt.Errorf("decode nft acknowledgement: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return dto.NftFlowAckDto{}, errors.New("nft acknowledgement contains trailing data")
	}
	return ack, nil
}

func acquireReporterLock(path string) (func(), error) {
	release, err := nftgeneration.AcquireLock(path)
	if err != nil {
		if errors.Is(err, nftgeneration.ErrLocked) {
			return nil, errReporterLocked
		}
		return nil, err
	}
	return func() { _ = release() }, nil
}

func buildBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out
}
