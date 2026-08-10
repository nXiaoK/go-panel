package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

type refreshRequest struct {
	Rules        []string
	ServerAddr   string
	Secret       string
	CurrentTable string
	TargetTable  string
}

func (r reporter) refreshRules(ctx context.Context, request refreshRequest) error {
	if r.store == nil || r.runtime == nil {
		return errors.New("refresh nft rules: reporter dependencies are incomplete")
	}
	if request.ServerAddr != "" {
		r.serverAddr = request.ServerAddr
	}
	if request.Secret != "" {
		r.secret = request.Secret
	}
	journal, err := r.store.load()
	if err != nil {
		return fmt.Errorf("load nft flow journal before refresh: %w", err)
	}
	hadGenerationRecovery := journal.Handoff != nil || journal.CleanupTable != "" || journal.MarkerPending
	handled, err := r.reconcileAndRecover(ctx, &journal)
	if err != nil {
		return err
	}
	if hadGenerationRecovery || handled {
		return nil
	}
	if request.CurrentTable != "" {
		if err := nftgeneration.ValidateTableName(request.CurrentTable); err != nil {
			return fmt.Errorf("invalid configured nft table: %w", err)
		}
	}
	// The journal and reconciled kernel inventory are authoritative after the
	// first generation switch. CurrentTable remains a compatibility/bootstrap
	// argument for old apply scripts which keep passing "flux_panel".
	currentTable := journal.ActiveTable
	targetTable := request.TargetTable
	if targetTable == "" {
		targetTable, err = nftgeneration.NewTableName(rand.Reader)
		if err != nil {
			return err
		}
	}
	if err := nftgeneration.ValidateTableName(targetTable); err != nil || targetTable == nftgeneration.LegacyTable || targetTable == currentTable {
		return fmt.Errorf("invalid refresh target table %q", targetTable)
	}
	if err := r.runtime.Probe(ctx); err != nil {
		return fmt.Errorf("verify nft generation capability: %w", err)
	}
	if err := r.runtime.Stage(ctx, targetTable, request.Rules); err != nil {
		return fmt.Errorf("stage nft generation: %w", err)
	}
	if err := r.runtime.Switch(ctx, currentTable, targetTable); err != nil {
		// nft may have committed an atomic transaction while its result was lost.
		// Only a discovery proving the target active may enter handoff recovery.
		tables, discoverErr := r.runtime.Discover(ctx)
		if discoverErr == nil && isSwitchedInventory(tables, currentTable, targetTable) {
			if _, recoverErr := r.reconcileAndRecover(ctx, &journal); recoverErr == nil {
				return nil
			} else {
				return errors.Join(fmt.Errorf("switch nft generation result was unknown: %w", err), recoverErr)
			}
		}
		return errors.Join(fmt.Errorf("switch nft generation: %w", err), discoverErr)
	}
	next := journal
	handoffStart := journal.LastSequence
	next.Handoff = &generationHandoff{
		StartSequence: &handoffStart,
		RetiredTable:  currentTable, TargetTable: targetTable,
		RetiredBaseline: canonicalJournalCounters(journal.Baseline),
	}
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("save nft generation handoff: %w", err)
	}
	journal = next
	if _, err := r.reconcileAndRecover(ctx, &journal); err != nil {
		return err
	}
	return nil
}

func (r reporter) recoverRefresh(ctx context.Context, journal *reporterJournal) error {
	_, err := r.reconcileAndRecover(ctx, journal)
	return err
}

// reconcileAndRecover always discovers kernel ownership before reading a
// retired snapshot, staging, uploading, deleting, or writing the marker.
func (r reporter) reconcileAndRecover(ctx context.Context, journal *reporterJournal) (bool, error) {
	if journal == nil || r.runtime == nil || r.store == nil {
		return false, errors.New("recover nft refresh: reporter dependencies are incomplete")
	}
	if err := validateJournal(*journal); err != nil {
		return false, fmt.Errorf("recover nft refresh journal: %w", err)
	}
	tables, err := r.runtime.Discover(ctx)
	if err != nil {
		return false, fmt.Errorf("discover nft generations: %w", err)
	}
	active, dormant, err := classifyGenerationInventory(tables)
	if err != nil {
		return false, err
	}
	if active == "" {
		if !isPristineBootstrapJournal(*journal) {
			return false, errors.New("no active nft generation for non-pristine journal")
		}
		if dormant != "" {
			if dormant == nftgeneration.LegacyTable {
				return false, errors.New("pristine bootstrap found unexpected dormant legacy table")
			}
			if err := r.deleteAndConfirm(ctx, dormant); err != nil {
				return false, fmt.Errorf("delete unreferenced bootstrap staging generation: %w", err)
			}
		}
		return false, nil
	}
	if journal.MarkerPending {
		if active != journal.ActiveTable || dormant != "" {
			return true, errors.New("pending marker journal contradicts nft generations")
		}
		if err := r.writeMarkerAndClear(journal); err != nil {
			return true, err
		}
		return true, nil
	}

	if journal.CleanupTable != "" {
		if active != journal.ActiveTable {
			return true, errors.New("cleanup journal contradicts active nft generation")
		}
		if dormant != "" && dormant != journal.CleanupTable {
			return true, errors.New("cleanup journal contradicts retired nft generation")
		}
		if dormant == journal.CleanupTable {
			if err := r.deleteAndConfirm(ctx, journal.CleanupTable); err != nil {
				return true, err
			}
		}
		next := *journal
		next.CleanupTable = ""
		next.MarkerPending = true
		if err := r.store.save(next); err != nil {
			return true, fmt.Errorf("clear nft cleanup journal: %w", err)
		}
		*journal = next
		if err := r.writeMarkerAndClear(journal); err != nil {
			return true, err
		}
		return true, nil
	}

	if journal.Handoff == nil && active != journal.ActiveTable {
		if dormant != journal.ActiveTable || journal.Pending != nil || journal.DrainSnapshot != nil {
			return false, errors.New("flow journal contradicts discovered nft ownership")
		}
		next := *journal
		handoffStart := journal.LastSequence
		next.Handoff = &generationHandoff{
			StartSequence: &handoffStart,
			RetiredTable:  journal.ActiveTable, TargetTable: active,
			RetiredBaseline: canonicalJournalCounters(journal.Baseline),
		}
		if err := r.store.save(next); err != nil {
			return true, fmt.Errorf("reconstruct switched nft handoff: %w", err)
		}
		*journal = next
	}

	if journal.Handoff != nil {
		handoff := journal.Handoff
		if active != handoff.TargetTable || dormant != handoff.RetiredTable {
			return true, errors.New("handoff journal contradicts discovered nft generations")
		}
		if err := r.drainHandoff(ctx, journal); err != nil {
			return true, err
		}
		return true, nil
	}

	if active != journal.ActiveTable {
		return false, errors.New("durable active table is not the unique active nft generation")
	}
	if dormant != "" {
		if journal.Pending != nil || journal.DrainSnapshot != nil {
			return true, errors.New("steady drain cannot own an unrelated dormant generation")
		}
		if dormant == nftgeneration.LegacyTable {
			return false, errors.New("generated active table cannot treat dormant legacy as staged")
		}
		if err := r.deleteAndConfirm(ctx, dormant); err != nil {
			return false, fmt.Errorf("delete unreferenced staged nft generation: %w", err)
		}
	}
	if journal.Pending != nil || journal.DrainSnapshot != nil {
		if err := r.drainSteadySnapshot(journal); err != nil {
			return true, err
		}
		if err := r.writeMarker(journal.ActiveTable); err != nil {
			return true, err
		}
		return false, nil
	}
	if err := r.writeMarker(journal.ActiveTable); err != nil {
		return false, err
	}
	return false, nil
}

func (r reporter) drainSteadySnapshot(journal *reporterJournal) error {
	if journal.Pending != nil {
		if err := r.deliverPending(r.serverAddr, r.secret, journal); err != nil {
			return err
		}
	}
	if journal.DrainSnapshot == nil {
		return nil
	}
	snapshot := journalToCounters(journal.DrainSnapshot)
	capturedAt := journal.DrainCapturedAt
	if capturedAt == 0 {
		capturedAt = r.capturedAtMillis()
	}
	for {
		items, resulting, ok := nextBoundedBatch(journalToCounters(journal.Baseline), snapshot)
		if !ok {
			next := *journal
			next.Baseline = countersToJournal(snapshot)
			next.DrainSnapshot = nil
			next.DrainInitialBaseline = nil
			next.DrainStartSequence = nil
			next.DrainCapturedAt = 0
			if err := r.store.save(next); err != nil {
				return fmt.Errorf("finish steady nft snapshot drain: %w", err)
			}
			*journal = next
			return nil
		}
		if err := r.persistAndDeliver(journal, items, resulting, capturedAt); err != nil {
			return err
		}
	}
}

func (r reporter) drainHandoff(ctx context.Context, journal *reporterJournal) error {
	if journal.Handoff.FrozenSnapshot == nil {
		snapshot, err := waitStableSnapshot(ctx, r.runtime, journal.Handoff.RetiredTable)
		if err != nil {
			return fmt.Errorf("freeze retired nft counters: %w", err)
		}
		if len(snapshot) > 0 {
			next := *journal
			handoff := *next.Handoff
			handoff.FrozenSnapshot = countersToJournal(snapshot)
			handoff.FrozenCapturedAt = r.capturedAtMillis()
			next.Handoff = &handoff
			if err := r.store.save(next); err != nil {
				return fmt.Errorf("save frozen retired nft snapshot: %w", err)
			}
			*journal = next
		}
	}
	if journal.Pending != nil {
		if err := r.deliverPending(r.serverAddr, r.secret, journal); err != nil {
			return err
		}
	}
	if journal.Handoff.FrozenSnapshot != nil {
		snapshot := journalToCounters(journal.Handoff.FrozenSnapshot)
		capturedAt := journal.Handoff.FrozenCapturedAt
		if capturedAt == 0 {
			capturedAt = r.capturedAtMillis()
		}
		for {
			items, resulting, ok := nextBoundedBatch(journalToCounters(journal.Handoff.RetiredBaseline), snapshot)
			if !ok {
				break
			}
			if err := r.persistAndDeliver(journal, items, resulting, capturedAt); err != nil {
				return err
			}
		}
	}
	retired, target := journal.Handoff.RetiredTable, journal.Handoff.TargetTable
	next := *journal
	next.ActiveTable = target
	next.Baseline = []journalCounter{}
	next.Handoff = nil
	next.Pending = nil
	next.DrainSnapshot = nil
	next.DrainInitialBaseline = nil
	next.DrainStartSequence = nil
	next.DrainCapturedAt = 0
	next.CleanupTable = retired
	next.MarkerPending = false
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("save completed nft handoff before cleanup: %w", err)
	}
	*journal = next
	if err := r.deleteAndConfirm(ctx, retired); err != nil {
		return err
	}
	next = *journal
	next.CleanupTable = ""
	next.MarkerPending = true
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("clear retired nft cleanup state: %w", err)
	}
	*journal = next
	if err := r.writeMarkerAndClear(journal); err != nil {
		return err
	}
	return nil
}

func (r reporter) persistAndDeliver(journal *reporterJournal, items []dto.NftFlowItem, resulting map[flowKey]counters, capturedAt int64) error {
	if journal.LastSequence >= math.MaxInt64-1 {
		return errors.New("nft reporter sequence exhausted")
	}
	batchID, err := randomReporterToken()
	if err != nil {
		return err
	}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: journal.LastSequence + 1,
		BatchID: batchID, CapturedAt: capturedAt, Items: items,
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	next := *journal
	if next.Handoff != nil {
		handoff := *next.Handoff
		handoff.FrozenCapturedAt = capturedAt
		next.Handoff = &handoff
	} else {
		next.DrainCapturedAt = capturedAt
	}
	next.Pending = &pendingReporterBatch{Payload: payload, ResultingBaseline: countersToJournal(resulting)}
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("save pending nft handoff batch: %w", err)
	}
	*journal = next
	return r.deliverPending(r.serverAddr, r.secret, journal)
}

func (r reporter) deleteAndConfirm(ctx context.Context, table string) error {
	deleteErr := r.runtime.Delete(ctx, table)
	tables, discoverErr := r.runtime.Discover(ctx)
	if discoverErr != nil {
		return errors.Join(deleteErr, fmt.Errorf("confirm nft generation deletion: %w", discoverErr))
	}
	for _, discovered := range tables {
		if discovered.Name == table {
			if deleteErr != nil {
				return deleteErr
			}
			return fmt.Errorf("nft generation %s remains after deletion", table)
		}
	}
	return nil
}

func (r reporter) writeMarker(table string) error {
	write := r.writeActiveMarker
	if write == nil {
		write = func(table string) error {
			return nftgeneration.WriteActiveMarker(nftgeneration.ActiveTableMarkerPath, table)
		}
	}
	if err := write(table); err != nil {
		return fmt.Errorf("write active nft table marker: %w", err)
	}
	return nil
}

func (r reporter) writeMarkerAndClear(journal *reporterJournal) error {
	if journal == nil || !journal.MarkerPending {
		return errors.New("active marker is not durably pending")
	}
	if err := r.writeMarker(journal.ActiveTable); err != nil {
		return err
	}
	next := *journal
	next.MarkerPending = false
	if err := r.store.save(next); err != nil {
		return fmt.Errorf("clear durable active marker work: %w", err)
	}
	*journal = next
	return nil
}

func classifyGenerationInventory(tables []GenerationTable) (active, dormant string, err error) {
	seen := map[string]struct{}{}
	for _, table := range tables {
		if nftgeneration.ValidateTableName(table.Name) != nil {
			return "", "", fmt.Errorf("unknown owned nft table %q", table.Name)
		}
		if _, duplicate := seen[table.Name]; duplicate {
			return "", "", fmt.Errorf("duplicate discovered nft table %q", table.Name)
		}
		seen[table.Name] = struct{}{}
		if table.Dormant {
			if dormant != "" {
				return "", "", errors.New("multiple retired or staged nft generations")
			}
			dormant = table.Name
		} else {
			if active != "" {
				return "", "", errors.New("multiple active nft generations")
			}
			active = table.Name
		}
	}
	return active, dormant, nil
}

func isPristineBootstrapJournal(journal reporterJournal) bool {
	return journal.Version == reporterJournalVersion && journal.ActiveTable == nftgeneration.LegacyTable &&
		journal.LastSequence == 0 && len(journal.Baseline) == 0 && journal.DrainSnapshot == nil &&
		len(journal.DrainInitialBaseline) == 0 && journal.DrainStartSequence == nil &&
		journal.Handoff == nil && journal.Pending == nil && journal.CleanupTable == "" && !journal.MarkerPending
}

func isSwitchedInventory(tables []GenerationTable, oldTable, newTable string) bool {
	active, dormant, err := classifyGenerationInventory(tables)
	return err == nil && active == newTable && dormant == oldTable &&
		slices.ContainsFunc(tables, func(table GenerationTable) bool { return table.Name == newTable })
}
