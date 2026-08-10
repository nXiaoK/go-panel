package main

import (
	"bytes"
	"container/heap"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

const (
	reporterJournalVersion = 3
	legacyJournalVersion   = 2
	maxJournalFileBytes    = 64 << 20
	maxJournalCounterRows  = 100_000
	maxPendingPayloadBytes = 2 << 20
	maxJournalJSONDepth    = 128
)

var reporterTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,80}$`)

type journalCounter struct {
	ForwardID    int64 `json:"forwardId"`
	UserID       int64 `json:"userId"`
	UserTunnelID int64 `json:"userTunnelId"`
	Up           int64 `json:"up"`
	Down         int64 `json:"down"`
}

type pendingReporterBatch struct {
	Payload           json.RawMessage  `json:"payload"`
	ResultingBaseline []journalCounter `json:"resultingBaseline"`
}

type generationHandoff struct {
	StartSequence   *uint64          `json:"startSequence"`
	RetiredTable    string           `json:"retiredTable"`
	TargetTable     string           `json:"targetTable"`
	RetiredBaseline []journalCounter `json:"retiredBaseline"`
	FrozenSnapshot  []journalCounter `json:"frozenSnapshot,omitempty"`
	// FrozenCapturedAt 与冻结快照原子持久化，保证切换恢复后的所有分片仍归属原采集小时。
	FrozenCapturedAt int64 `json:"frozenCapturedAt,omitempty"`
}

type reporterJournal struct {
	Version      int              `json:"version"`
	ReporterID   string           `json:"reporterId"`
	LastSequence uint64           `json:"lastSequence"`
	ActiveTable  string           `json:"activeTable"`
	Baseline     []journalCounter `json:"baseline"`
	// DrainSnapshot is a strict v3 extension which preserves Task 2's fixed
	// active-table snapshot across crashes while a normal reporter drain is in
	// progress. Handoff drains use Handoff.FrozenSnapshot instead.
	DrainSnapshot        []journalCounter `json:"drainSnapshot,omitempty"`
	DrainInitialBaseline []journalCounter `json:"drainInitialBaseline,omitempty"`
	DrainStartSequence   *uint64          `json:"drainStartSequence,omitempty"`
	// DrainCapturedAt 是固定快照的采集时间；零值仅兼容升级前尚未排空的旧 journal。
	DrainCapturedAt int64                 `json:"drainCapturedAt,omitempty"`
	Handoff         *generationHandoff    `json:"handoff,omitempty"`
	Pending         *pendingReporterBatch `json:"pending,omitempty"`
	CleanupTable    string                `json:"cleanupTable,omitempty"`
	MarkerPending   bool                  `json:"markerPending,omitempty"`
}

type legacyV2Journal struct {
	Version       int                   `json:"version"`
	ReporterID    string                `json:"reporterId"`
	LastSequence  uint64                `json:"lastSequence"`
	Baseline      []journalCounter      `json:"baseline"`
	DrainSnapshot []journalCounter      `json:"drainSnapshot,omitempty"`
	Pending       *pendingReporterBatch `json:"pending,omitempty"`
}

type journalStore interface {
	load() (reporterJournal, error)
	save(reporterJournal) error
}

type reporter struct {
	store             journalStore
	readCounters      func(string) (map[flowKey]counters, error)
	upload            func(string, string, []byte) (dto.NftFlowAckDto, error)
	runtime           nftRuntime
	serverAddr        string
	secret            string
	writeActiveMarker func(string) error
	now               func() time.Time
}

func (r reporter) capturedAtMillis() int64 {
	if r.now != nil {
		return r.now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (r reporter) runOnce(serverAddr, secret, table string) error {
	journal, err := r.store.load()
	if err != nil {
		return fmt.Errorf("load nft flow journal: %w", err)
	}
	if journal.Handoff != nil || journal.CleanupTable != "" || journal.MarkerPending {
		return errors.New("nft generation refresh is unresolved; run refresh recovery before active reporting")
	}
	if journal.Pending != nil {
		if err := r.deliverPending(serverAddr, secret, &journal); err != nil {
			return err
		}
	}
	var snapshot map[flowKey]counters
	capturedAt := journal.DrainCapturedAt
	if journal.DrainSnapshot != nil {
		snapshot = journalToCounters(journal.DrainSnapshot)
	} else {
		current, err := r.readCounters(table)
		if err != nil {
			return fmt.Errorf("read nft counters: %w", err)
		}
		snapshot = cloneCounters(current)
		capturedAt = r.capturedAtMillis()
	}
	if capturedAt == 0 {
		// 旧版正在排空的快照没有持久化采集时间，只能从本次升级恢复时开始标记；
		// 后续新快照都会在首个 Pending 批次中原子保存真实采集时间。
		capturedAt = r.capturedAtMillis()
	}
	for {
		items, nextBaseline, ok := nextBoundedBatch(journalToCounters(journal.Baseline), snapshot)
		if !ok {
			// Keys which disappeared from the snapshot have no delta to upload. Only
			// after every bounded delta is acknowledged can the baseline be replaced
			// exactly, dropping those stale keys.
			journal.Baseline = countersToJournal(snapshot)
			journal.DrainSnapshot = nil
			journal.DrainInitialBaseline = nil
			journal.DrainStartSequence = nil
			journal.DrainCapturedAt = 0
			if err := r.store.save(journal); err != nil {
				return fmt.Errorf("save nft flow baseline: %w", err)
			}
			return nil
		}
		batchID, err := randomReporterToken()
		if err != nil {
			return fmt.Errorf("generate nft batch id: %w", err)
		}
		if journal.LastSequence >= math.MaxInt64-1 {
			return errors.New("nft reporter sequence exhausted")
		}
		batch := dto.NftFlowBatchV2Dto{
			ReporterID: journal.ReporterID, Sequence: journal.LastSequence + 1, BatchID: batchID,
			CapturedAt: capturedAt, Items: items,
		}
		payload, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		journal.Pending = &pendingReporterBatch{
			Payload: append(json.RawMessage(nil), payload...), ResultingBaseline: countersToJournal(nextBaseline),
		}
		if journal.DrainSnapshot == nil {
			// Persist the complete immutable snapshot in the same atomic journal
			// replacement as its first Pending batch. A crash can then resume the
			// remaining chunks without consulting mutable live counters.
			start := journal.LastSequence
			journal.DrainSnapshot = countersToJournal(snapshot)
			journal.DrainInitialBaseline = canonicalJournalCounters(journal.Baseline)
			journal.DrainStartSequence = &start
		}
		journal.DrainCapturedAt = capturedAt
		if err := r.store.save(journal); err != nil {
			return fmt.Errorf("save pending nft batch: %w", err)
		}
		if err := r.deliverPending(serverAddr, secret, &journal); err != nil {
			return err
		}
	}
}

func (r reporter) deliverPending(serverAddr, secret string, journal *reporterJournal) error {
	pending := journal.Pending
	if pending == nil {
		return errors.New("missing pending nft batch")
	}
	var batch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(pending.Payload, &batch); err != nil {
		return fmt.Errorf("decode pending nft batch: %w", err)
	}
	ack, err := r.upload(serverAddr, secret, pending.Payload)
	if err != nil {
		return fmt.Errorf("upload nft flow: %w", err)
	}
	if err := verifyNftFlowAck(batch, ack); err != nil {
		return err
	}
	next := *journal
	next.LastSequence = batch.Sequence
	if next.Handoff != nil {
		handoff := *next.Handoff
		handoff.RetiredBaseline = canonicalJournalCounters(pending.ResultingBaseline)
		next.Handoff = &handoff
	} else {
		next.Baseline = canonicalJournalCounters(pending.ResultingBaseline)
	}
	next.Pending = nil
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("save acknowledged nft batch: %w", err)
	}
	*journal = next
	return nil
}

// nextBoundedBatch deterministically advances a partial baseline toward one
// fixed counter snapshot without exceeding either protocol limit.
func nextBoundedBatch(previous, snapshot map[flowKey]counters) ([]dto.NftFlowItem, map[flowKey]counters, bool) {
	next := cloneCounters(previous)
	keys := make([]flowKey, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ForwardID != keys[j].ForwardID {
			return keys[i].ForwardID < keys[j].ForwardID
		}
		if keys[i].UserID != keys[j].UserID {
			return keys[i].UserID < keys[j].UserID
		}
		return keys[i].UserTunnelID < keys[j].UserTunnelID
	})

	items := make([]dto.NftFlowItem, 0, min(len(keys), dto.MaxNftFlowBatchItems))
	for _, key := range keys {
		if len(items) == dto.MaxNftFlowBatchItems {
			break
		}
		current := snapshot[key]
		prior := previous[key]
		up, nextUp := boundedDirection(prior.Up, current.Up)
		down, nextDown := boundedDirection(prior.Down, current.Down)
		if up == 0 && down == 0 {
			continue
		}
		advanced := prior
		if up > 0 {
			advanced.Up = nextUp
		}
		if down > 0 {
			advanced.Down = nextDown
		}
		next[key] = advanced
		forwardID, userID, userTunnelID := key.ForwardID, key.UserID, key.UserTunnelID
		items = append(items, dto.NftFlowItem{
			ForwardID: &forwardID, UserID: &userID, UserTunnelID: &userTunnelID, Up: &up, Down: &down,
		})
	}
	return items, next, len(items) > 0
}

func boundedDirection(previous, current int64) (amount, resulting int64) {
	base := previous
	if current < previous {
		base = 0
	}
	remaining := current - base
	if remaining <= 0 {
		return 0, previous
	}
	amount = min(remaining, dto.MaxNftFlowItemBytes)
	return amount, base + amount
}

func cloneCounters(state map[flowKey]counters) map[flowKey]counters {
	out := make(map[flowKey]counters, len(state))
	for key, value := range state {
		out[key] = value
	}
	return out
}

func verifyNftFlowAck(batch dto.NftFlowBatchV2Dto, ack dto.NftFlowAckDto) error {
	digest, err := dto.NftFlowBatchDigest(batch)
	if err != nil {
		return err
	}
	if ack.ReporterID != batch.ReporterID || ack.Sequence != batch.Sequence || ack.BatchID != batch.BatchID || ack.AckDigest != digest {
		return errors.New("nft flow acknowledgement does not match pending batch")
	}
	return nil
}

type fileJournalStore struct {
	path         string
	saveOverride func(reporterJournal) error
}

func (s fileJournalStore) load() (reporterJournal, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		journal, err := newReporterJournal()
		if err != nil {
			return reporterJournal{}, err
		}
		if err := s.save(journal); err != nil {
			return reporterJournal{}, err
		}
		return journal, nil
	}
	if err != nil {
		return reporterJournal{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxJournalFileBytes+1))
	if err != nil {
		return reporterJournal{}, fmt.Errorf("read nft flow journal: %w", err)
	}
	if len(raw) > maxJournalFileBytes {
		return reporterJournal{}, errors.New("nft flow journal exceeds size limit")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return reporterJournal{}, errors.New("empty nft flow journal")
	}
	if err := rejectAmbiguousJournalJSON(trimmed); err != nil {
		return reporterJournal{}, fmt.Errorf("decode flow journal: %w", err)
	}
	if trimmed[0] == '[' {
		var legacy []journalCounter
		if err := decodeStrictJournalJSON(trimmed, &legacy); err != nil {
			return reporterJournal{}, fmt.Errorf("decode legacy flow journal: %w", err)
		}
		if err := validateJournalCounters(legacy); err != nil {
			return reporterJournal{}, fmt.Errorf("decode legacy flow journal: %w", err)
		}
		journal, err := newReporterJournal()
		if err != nil {
			return reporterJournal{}, err
		}
		journal.Baseline = canonicalJournalCounters(legacy)
		if err := validateJournal(journal); err != nil {
			return reporterJournal{}, err
		}
		if err := s.save(journal); err != nil {
			return reporterJournal{}, err
		}
		return journal, nil
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return reporterJournal{}, fmt.Errorf("decode flow journal version: %w", err)
	}
	if probe.Version == legacyJournalVersion {
		var legacy legacyV2Journal
		if err := decodeStrictJournalJSON(trimmed, &legacy); err != nil {
			return reporterJournal{}, fmt.Errorf("decode version-2 flow journal: %w", err)
		}
		migrated, err := migrateV2Journal(legacy)
		if err != nil {
			return reporterJournal{}, fmt.Errorf("migrate version-2 flow journal: %w", err)
		}
		if err := s.save(migrated); err != nil {
			return reporterJournal{}, fmt.Errorf("save migrated version-2 flow journal: %w", err)
		}
		return migrated, nil
	}
	var journal reporterJournal
	if err := decodeStrictJournalJSON(trimmed, &journal); err != nil {
		return reporterJournal{}, fmt.Errorf("decode flow journal: %w", err)
	}
	if err := validateJournal(journal); err != nil {
		return reporterJournal{}, err
	}
	return journal, nil
}

func (s fileJournalStore) save(journal reporterJournal) error {
	if err := validateJournal(journal); err != nil {
		return err
	}
	if s.saveOverride != nil {
		return s.saveOverride(journal)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(raw) > maxJournalFileBytes {
		return errors.New("nft flow journal exceeds size limit")
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".flow-journal-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

type journalJSONFrame struct {
	object       bool
	expectingKey bool
	keys         map[string]struct{}
	path         string
	valueKey     string
	counterRows  bool
	arrayCount   int
}

func rejectAmbiguousJournalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	stack := make([]journalJSONFrame, 0, 8)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if len(stack) != 0 {
				return errors.New("unterminated journal JSON")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if len(stack) > 0 && !stack[len(stack)-1].object && stack[len(stack)-1].counterRows {
			closing := false
			if delimiter, ok := token.(json.Delim); ok && delimiter == ']' {
				closing = true
			}
			if !closing {
				top := &stack[len(stack)-1]
				top.arrayCount++
				if top.arrayCount > maxJournalCounterRows {
					return fmt.Errorf("predecode counter row limit exceeded at %s", top.path)
				}
			}
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				if len(stack) >= maxJournalJSONDepth {
					return errors.New("journal JSON nesting is too deep")
				}
				stack = append(stack, journalJSONFrame{object: true, expectingKey: true, keys: map[string]struct{}{}, path: nextJournalJSONPath(stack)})
			case '[':
				if len(stack) >= maxJournalJSONDepth {
					return errors.New("journal JSON nesting is too deep")
				}
				path := nextJournalJSONPath(stack)
				stack = append(stack, journalJSONFrame{path: path, counterRows: isJournalCounterArrayPath(path)})
			case '}', ']':
				if len(stack) == 0 || value == '}' && !stack[len(stack)-1].object || value == ']' && stack[len(stack)-1].object {
					return errors.New("malformed journal JSON close token")
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectingKey = true
					stack[len(stack)-1].valueKey = ""
				}
			}
		case string:
			if len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].expectingKey {
				top := &stack[len(stack)-1]
				if _, duplicate := top.keys[value]; duplicate {
					return fmt.Errorf("duplicate journal JSON key %q", value)
				}
				top.keys[value] = struct{}{}
				top.expectingKey = false
				top.valueKey = value
			} else if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].expectingKey = true
				stack[len(stack)-1].valueKey = ""
			}
		default:
			if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].expectingKey = true
				stack[len(stack)-1].valueKey = ""
			}
		}
	}
}

func nextJournalJSONPath(stack []journalJSONFrame) string {
	if len(stack) == 0 {
		return "$"
	}
	parent := stack[len(stack)-1]
	if parent.object {
		return parent.path + "." + parent.valueKey
	}
	return parent.path + "[]"
}

func isJournalCounterArrayPath(path string) bool {
	switch path {
	case "$", "$.baseline", "$.drainSnapshot", "$.drainInitialBaseline",
		"$.handoff.retiredBaseline", "$.handoff.frozenSnapshot", "$.pending.resultingBaseline":
		return true
	default:
		return false
	}
}

func decodeStrictJournalJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("flow journal contains trailing data")
	}
	return nil
}

func migrateV2Journal(legacy legacyV2Journal) (reporterJournal, error) {
	if legacy.Version != legacyJournalVersion || !reporterTokenPattern.MatchString(legacy.ReporterID) || legacy.LastSequence >= math.MaxInt64 {
		return reporterJournal{}, errors.New("invalid version-2 flow journal identity")
	}
	if err := validateJournalCounters(legacy.Baseline); err != nil {
		return reporterJournal{}, err
	}
	if legacy.DrainSnapshot != nil {
		if len(legacy.DrainSnapshot) == 0 {
			return reporterJournal{}, errors.New("invalid empty version-2 drain snapshot")
		}
		if err := validateJournalCounters(legacy.DrainSnapshot); err != nil {
			return reporterJournal{}, errors.New("invalid version-2 drain snapshot")
		}
	}
	migrated := reporterJournal{
		Version: reporterJournalVersion, ReporterID: legacy.ReporterID, LastSequence: legacy.LastSequence,
		ActiveTable:   nftgeneration.LegacyTable,
		Baseline:      canonicalJournalCounters(legacy.Baseline),
		DrainSnapshot: canonicalJournalCountersOrNil(legacy.DrainSnapshot),
		Pending:       clonePending(legacy.Pending),
	}
	if migrated.DrainSnapshot != nil {
		start := migrated.LastSequence
		migrated.DrainInitialBaseline = canonicalJournalCounters(migrated.Baseline)
		migrated.DrainStartSequence = &start
	}
	if migrated.Pending != nil && migrated.DrainSnapshot == nil {
		var err error
		migrated, err = migrateLegacyPending(migrated)
		if err != nil {
			return reporterJournal{}, err
		}
	}
	if err := validateJournal(migrated); err != nil {
		return reporterJournal{}, err
	}
	return migrated, nil
}

func clonePending(pending *pendingReporterBatch) *pendingReporterBatch {
	if pending == nil {
		return nil
	}
	return &pendingReporterBatch{
		Payload:           append(json.RawMessage(nil), pending.Payload...),
		ResultingBaseline: canonicalJournalCounters(pending.ResultingBaseline),
	}
}

func canonicalJournalCountersOrNil(rows []journalCounter) []journalCounter {
	if rows == nil {
		return nil
	}
	return canonicalJournalCounters(rows)
}

func migrateLegacyPending(journal reporterJournal) (reporterJournal, error) {
	if journal.Version != reporterJournalVersion || !reporterTokenPattern.MatchString(journal.ReporterID) || journal.Pending == nil {
		return reporterJournal{}, errors.New("invalid legacy flow journal identity")
	}
	if err := validateJournalCounters(journal.Baseline); err != nil {
		return reporterJournal{}, err
	}
	if err := validateJournalCounters(journal.Pending.ResultingBaseline); err != nil {
		return reporterJournal{}, errors.New("invalid legacy pending resulting baseline")
	}
	batch, err := decodeCanonicalPendingBatch(journal)
	if err != nil {
		return reporterJournal{}, err
	}
	if len(batch.Items) == 0 {
		return reporterJournal{}, errors.New("invalid empty legacy pending flow batch")
	}
	oversized := len(batch.Items) > dto.MaxNftFlowBatchItems
	for _, item := range batch.Items {
		if err := validatePendingItem(item, false); err != nil {
			return reporterJournal{}, err
		}
		if *item.Up > dto.MaxNftFlowItemBytes || *item.Down > dto.MaxNftFlowItemBytes {
			oversized = true
		}
	}

	previous := journalToCounters(journal.Baseline)
	snapshot := journalToCounters(journal.Pending.ResultingBaseline)
	derived := batch
	derived.Items = legacyFullDeltaItems(previous, snapshot)
	expectedDigest, err := dto.NftFlowBatchDigest(batch)
	if err != nil {
		return reporterJournal{}, errors.New("invalid legacy pending flow batch items")
	}
	derivedDigest, err := dto.NftFlowBatchDigest(derived)
	if err != nil || derivedDigest != expectedDigest {
		return reporterJournal{}, errors.New("legacy pending resulting baseline does not match payload")
	}

	migrated := journal
	migrated.DrainSnapshot = countersToJournal(snapshot)
	migrated.DrainInitialBaseline = canonicalJournalCounters(journal.Baseline)
	start := journal.LastSequence
	migrated.DrainStartSequence = &start
	if oversized {
		// The panel rejects this payload before committing it, so it is safe to
		// replace it with bounded batches starting at the same LastSequence.
		migrated.Pending = nil
		return migrated, nil
	}
	expectedItems, safeBaseline, ok := nextBoundedBatch(previous, snapshot)
	if !ok {
		return reporterJournal{}, errors.New("legacy pending flow batch has no delta")
	}
	derived.Items = expectedItems
	derivedDigest, err = dto.NftFlowBatchDigest(derived)
	if err != nil || derivedDigest != expectedDigest {
		return reporterJournal{}, errors.New("legacy pending flow batch is not one bounded snapshot step")
	}
	migrated.Pending = &pendingReporterBatch{
		Payload:           append(json.RawMessage(nil), journal.Pending.Payload...),
		ResultingBaseline: countersToJournal(safeBaseline),
	}
	return migrated, nil
}

func decodeCanonicalPendingBatch(journal reporterJournal) (dto.NftFlowBatchV2Dto, error) {
	if journal.Pending == nil || len(journal.Pending.Payload) == 0 {
		return dto.NftFlowBatchV2Dto{}, errors.New("missing pending flow batch")
	}
	var batch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(journal.Pending.Payload, &batch); err != nil ||
		batch.ReporterID != journal.ReporterID || !reporterTokenPattern.MatchString(batch.BatchID) ||
		batch.Sequence == 0 || batch.Sequence >= math.MaxInt64 || batch.Sequence != journal.LastSequence+1 {
		return dto.NftFlowBatchV2Dto{}, errors.New("invalid pending flow batch identity")
	}
	canonicalPayload, err := json.Marshal(batch)
	if err != nil || !bytes.Equal(canonicalPayload, journal.Pending.Payload) {
		return dto.NftFlowBatchV2Dto{}, errors.New("pending flow batch is not canonical JSON")
	}
	return batch, nil
}

func validatePendingItem(item dto.NftFlowItem, enforceProtocolLimit bool) error {
	if item.ForwardID == nil || item.UserID == nil || item.Up == nil || item.Down == nil {
		return errors.New("invalid pending flow batch items")
	}
	userTunnelID := int64(0)
	if item.UserTunnelID != nil {
		userTunnelID = *item.UserTunnelID
	}
	if *item.ForwardID <= 0 || *item.UserID <= 0 || userTunnelID < 0 || *item.Up < 0 || *item.Down < 0 {
		return errors.New("invalid pending flow batch items")
	}
	if enforceProtocolLimit && (*item.Up > dto.MaxNftFlowItemBytes || *item.Down > dto.MaxNftFlowItemBytes) {
		return errors.New("pending flow batch items exceed protocol limit")
	}
	return nil
}

func legacyFullDeltaItems(previous, snapshot map[flowKey]counters) []dto.NftFlowItem {
	items := make([]dto.NftFlowItem, 0, len(snapshot))
	for key, current := range snapshot {
		prior := previous[key]
		up := fullCounterDelta(current.Up, prior.Up)
		down := fullCounterDelta(current.Down, prior.Down)
		if up == 0 && down == 0 {
			continue
		}
		forwardID, userID, userTunnelID := key.ForwardID, key.UserID, key.UserTunnelID
		items = append(items, dto.NftFlowItem{
			ForwardID: &forwardID, UserID: &userID, UserTunnelID: &userTunnelID, Up: &up, Down: &down,
		})
	}
	return items
}

func fullCounterDelta(current, previous int64) int64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func newReporterJournal() (reporterJournal, error) {
	reporterID, err := randomReporterToken()
	if err != nil {
		return reporterJournal{}, err
	}
	return reporterJournal{
		Version: reporterJournalVersion, ReporterID: reporterID,
		ActiveTable: nftgeneration.LegacyTable, Baseline: []journalCounter{},
	}, nil
}

func validateJournal(journal reporterJournal) error {
	if journal.Version != reporterJournalVersion || !reporterTokenPattern.MatchString(journal.ReporterID) || journal.LastSequence >= math.MaxInt64 {
		return errors.New("invalid flow journal identity")
	}
	if err := nftgeneration.ValidateTableName(journal.ActiveTable); err != nil {
		return errors.New("invalid active flow journal table")
	}
	if err := validateJournalCounters(journal.Baseline); err != nil {
		return err
	}
	if journal.DrainSnapshot == nil {
		if len(journal.DrainInitialBaseline) != 0 || journal.DrainStartSequence != nil || journal.DrainCapturedAt != 0 {
			return errors.New("steady drain metadata exists without snapshot")
		}
	} else {
		if len(journal.DrainSnapshot) == 0 || journal.Handoff != nil || journal.CleanupTable != "" || journal.DrainCapturedAt < 0 {
			return errors.New("invalid empty drain snapshot")
		}
		if err := validateJournalCounters(journal.DrainSnapshot); err != nil {
			return errors.New("invalid drain snapshot")
		}
		if err := validateJournalCounters(journal.DrainInitialBaseline); err != nil {
			return errors.New("invalid drain initial baseline")
		}
		if journal.DrainStartSequence == nil || *journal.DrainStartSequence > journal.LastSequence {
			return errors.New("invalid drain start sequence")
		}
		expected, err := expectedSnapshotProgress(
			journal.DrainInitialBaseline, journal.DrainSnapshot,
			journal.LastSequence-*journal.DrainStartSequence,
		)
		if err != nil || !equalCounterMaps(expected, journalToCounters(journal.Baseline)) {
			return errors.New("steady drain baseline is not the exact acknowledged prefix")
		}
	}
	if journal.Handoff != nil {
		handoff := journal.Handoff
		if journal.CleanupTable != "" || journal.DrainSnapshot != nil ||
			nftgeneration.ValidateTableName(handoff.RetiredTable) != nil ||
			nftgeneration.ValidateTableName(handoff.TargetTable) != nil ||
			handoff.TargetTable == nftgeneration.LegacyTable || handoff.RetiredTable == handoff.TargetTable ||
			journal.ActiveTable != handoff.RetiredTable {
			return errors.New("invalid flow journal handoff tables")
		}
		if err := validateJournalCounters(handoff.RetiredBaseline); err != nil {
			return errors.New("invalid handoff retired baseline")
		}
		if handoff.StartSequence == nil || *handoff.StartSequence > journal.LastSequence {
			return errors.New("invalid handoff start sequence")
		}
		if handoff.FrozenSnapshot != nil {
			if len(handoff.FrozenSnapshot) == 0 || handoff.FrozenCapturedAt < 0 {
				return errors.New("invalid empty handoff frozen snapshot")
			}
			if err := validateJournalCounters(handoff.FrozenSnapshot); err != nil {
				return errors.New("invalid handoff frozen snapshot")
			}
			expected, err := expectedSnapshotProgress(
				journal.Baseline, handoff.FrozenSnapshot,
				journal.LastSequence-*handoff.StartSequence,
			)
			if err != nil || !equalCounterMaps(expected, journalToCounters(handoff.RetiredBaseline)) {
				return errors.New("handoff retired baseline is not the exact acknowledged prefix")
			}
		} else if handoff.FrozenCapturedAt != 0 || journal.LastSequence != *handoff.StartSequence ||
			!equalCounterMaps(journalToCounters(journal.Baseline), journalToCounters(handoff.RetiredBaseline)) {
			return errors.New("unfrozen handoff retired baseline changed")
		}
	}
	if journal.CleanupTable != "" {
		if journal.ActiveTable == nftgeneration.LegacyTable || nftgeneration.ValidateTableName(journal.CleanupTable) != nil || journal.CleanupTable == journal.ActiveTable ||
			journal.Handoff != nil || journal.Pending != nil || journal.DrainSnapshot != nil || len(journal.Baseline) != 0 {
			return errors.New("invalid flow journal cleanup state")
		}
	}
	if journal.MarkerPending && (journal.ActiveTable == nftgeneration.LegacyTable || journal.Handoff != nil || journal.Pending != nil || journal.DrainSnapshot != nil || journal.CleanupTable != "" || len(journal.Baseline) != 0) {
		return errors.New("invalid pending active marker state")
	}
	if journal.Pending != nil {
		if len(journal.Pending.Payload) == 0 || len(journal.Pending.Payload) > maxPendingPayloadBytes || journal.CleanupTable != "" {
			return errors.New("invalid pending flow batch payload size")
		}
		batch, err := decodeCanonicalPendingBatch(journal)
		if err != nil || len(batch.Items) == 0 || len(batch.Items) > dto.MaxNftFlowBatchItems {
			return errors.New("invalid pending flow batch")
		}
		if _, err := dto.NftFlowBatchDigest(batch); err != nil {
			return errors.New("invalid pending flow batch items")
		}
		for _, item := range batch.Items {
			if err := validatePendingItem(item, true); err != nil {
				return errors.New("invalid pending flow batch items")
			}
		}
		if err := validateJournalCounters(journal.Pending.ResultingBaseline); err != nil {
			return errors.New("invalid pending resulting baseline")
		}
		previousRows := journal.Baseline
		snapshotRows := journal.DrainSnapshot
		expectedCapturedAt := journal.DrainCapturedAt
		if journal.Handoff != nil {
			if journal.Handoff.FrozenSnapshot == nil {
				return errors.New("pending handoff batch has no frozen snapshot")
			}
			previousRows = journal.Handoff.RetiredBaseline
			snapshotRows = journal.Handoff.FrozenSnapshot
			expectedCapturedAt = journal.Handoff.FrozenCapturedAt
		} else if journal.DrainSnapshot == nil {
			return errors.New("pending steady batch has no drain snapshot")
		}
		if batch.CapturedAt != expectedCapturedAt {
			return errors.New("pending flow batch capture time does not match frozen snapshot")
		}
		previous := journalToCounters(previousRows)
		resulting := journalToCounters(journal.Pending.ResultingBaseline)
		expectedItems, expectedBaseline, ok := nextBoundedBatch(previous, journalToCounters(snapshotRows))
		if !ok || !equalCounterMaps(expectedBaseline, resulting) {
			return errors.New("pending resulting baseline is not the next bounded step")
		}
		expectedDigest, _ := dto.NftFlowBatchDigest(batch)
		derived := batch
		derived.Items = expectedItems
		derivedDigest, err := dto.NftFlowBatchDigest(derived)
		if err != nil || derivedDigest != expectedDigest {
			return errors.New("pending resulting baseline does not match payload")
		}
	}
	return nil
}

func validateJournalCounters(rows []journalCounter) error {
	if len(rows) > maxJournalCounterRows {
		return errors.New("flow journal counter row limit exceeded")
	}
	seen := make(map[flowKey]struct{}, len(rows))
	for _, row := range rows {
		if row.ForwardID <= 0 || row.UserID <= 0 || row.UserTunnelID < 0 || row.Up < 0 || row.Down < 0 {
			return errors.New("invalid flow journal baseline")
		}
		key := flowKey{ForwardID: row.ForwardID, UserID: row.UserID, UserTunnelID: row.UserTunnelID}
		if _, exists := seen[key]; exists {
			return errors.New("duplicate flow journal counter key")
		}
		seen[key] = struct{}{}
	}
	return nil
}

type batchAvailabilityHeap []uint64

func (h batchAvailabilityHeap) Len() int           { return len(h) }
func (h batchAvailabilityHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h batchAvailabilityHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *batchAvailabilityHeap) Push(value any)    { *h = append(*h, value.(uint64)) }
func (h *batchAvailabilityHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type snapshotProgressJob struct {
	key      flowKey
	current  counters
	services uint64
}

// expectedSnapshotProgress reconstructs exactly the baseline after a fixed
// number of complete ACKed batches. Deterministic batching is equivalent to
// FCFS jobs on MaxNftFlowBatchItems parallel slots: every selected key consumes
// one slot for max(upChunks, downChunks) consecutive batches. This avoids a
// per-chunk loop even at the int64 counter limit.
func expectedSnapshotProgress(initialRows, snapshotRows []journalCounter, acknowledged uint64) (map[flowKey]counters, error) {
	initial := journalToCounters(initialRows)
	snapshot := journalToCounters(snapshotRows)
	keys := make([]flowKey, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ForwardID != keys[j].ForwardID {
			return keys[i].ForwardID < keys[j].ForwardID
		}
		if keys[i].UserID != keys[j].UserID {
			return keys[i].UserID < keys[j].UserID
		}
		return keys[i].UserTunnelID < keys[j].UserTunnelID
	})
	jobs := make([]snapshotProgressJob, 0, len(keys))
	for _, key := range keys {
		start := initial[key]
		current := snapshot[key]
		services := max(counterDirectionChunks(start.Up, current.Up), counterDirectionChunks(start.Down, current.Down))
		if services > 0 {
			jobs = append(jobs, snapshotProgressJob{key: key, current: current, services: services})
		}
	}
	if len(jobs) == 0 {
		if acknowledged != 0 {
			return nil, errors.New("acknowledged batches exceed empty snapshot work")
		}
		return cloneCounters(initial), nil
	}
	slots := min(len(jobs), dto.MaxNftFlowBatchItems)
	availability := make(batchAvailabilityHeap, slots)
	for i := range availability {
		availability[i] = 1
	}
	heap.Init(&availability)
	expected := cloneCounters(initial)
	var finalBatch uint64
	for _, job := range jobs {
		entry := heap.Pop(&availability).(uint64)
		if job.services > math.MaxUint64-entry {
			return nil, errors.New("snapshot batch schedule overflow")
		}
		nextAvailable := entry + job.services
		heap.Push(&availability, nextAvailable)
		completion := nextAvailable - 1
		if completion > finalBatch {
			finalBatch = completion
		}
		var served uint64
		if acknowledged >= entry {
			served = acknowledged - entry + 1
			if served > job.services {
				served = job.services
			}
		}
		if served > 0 {
			start := initial[job.key]
			expected[job.key] = counters{
				Up:   advanceSnapshotDirection(start.Up, job.current.Up, served),
				Down: advanceSnapshotDirection(start.Down, job.current.Down, served),
			}
		}
	}
	if acknowledged > finalBatch {
		return nil, errors.New("acknowledged batches exceed snapshot work")
	}
	return expected, nil
}

func counterDirectionChunks(initial, current int64) uint64 {
	base := initial
	if current < initial {
		base = 0
	}
	delta := current - base
	if delta <= 0 {
		return 0
	}
	return uint64((delta-1)/dto.MaxNftFlowItemBytes + 1)
}

func advanceSnapshotDirection(initial, current int64, served uint64) int64 {
	chunks := counterDirectionChunks(initial, current)
	if served == 0 || chunks == 0 {
		return initial
	}
	if served >= chunks {
		return current
	}
	base := initial
	if current < initial {
		base = 0
	}
	return base + int64(served)*dto.MaxNftFlowItemBytes
}

func equalCounterMaps(left, right map[flowKey]counters) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func randomReporterToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func countersToJournal(state map[flowKey]counters) []journalCounter {
	rows := make([]journalCounter, 0, len(state))
	for key, value := range state {
		rows = append(rows, journalCounter{
			ForwardID: key.ForwardID, UserID: key.UserID, UserTunnelID: key.UserTunnelID,
			Up: value.Up, Down: value.Down,
		})
	}
	return canonicalJournalCounters(rows)
}

func canonicalJournalCounters(rows []journalCounter) []journalCounter {
	out := append([]journalCounter(nil), rows...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ForwardID != out[j].ForwardID {
			return out[i].ForwardID < out[j].ForwardID
		}
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].UserTunnelID < out[j].UserTunnelID
	})
	return out
}

func journalToCounters(rows []journalCounter) map[flowKey]counters {
	out := make(map[flowKey]counters, len(rows))
	for _, row := range rows {
		out[flowKey{ForwardID: row.ForwardID, UserID: row.UserID, UserTunnelID: row.UserTunnelID}] = counters{Up: row.Up, Down: row.Down}
	}
	return out
}
