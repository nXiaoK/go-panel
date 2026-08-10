package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"
)

const (
	stableSnapshotInterval = 50 * time.Millisecond
	stableSnapshotTimeout  = 2 * time.Second
)

// GenerationTable is one Flux-owned nft table. Dormant tables retain their
// rules and counters but have no registered base-chain hooks.
type GenerationTable struct {
	Name    string
	Dormant bool
}

// nftRuntime is the complete kernel boundary used by generation handoff. It
// deliberately exposes Switch as one operation so callers cannot accidentally
// split traffic ownership across two nft transactions.
type nftRuntime interface {
	Probe(context.Context) error
	Discover(context.Context) ([]GenerationTable, error)
	Stage(context.Context, string, []string) error
	Switch(context.Context, string, string) error
	ReadCounters(context.Context, string) (map[flowKey]counters, error)
	Delete(context.Context, string) error
}

// stageAndSwitchGeneration keeps capability failure and staging failure before
// the ownership linearization point. A switch error deliberately leaves both
// generations for later reconciliation; this layer never deletes either one.
func stageAndSwitchGeneration(ctx context.Context, runtime nftRuntime, oldTable, newTable string, rules []string) error {
	if runtime == nil {
		return errors.New("nil nft generation runtime")
	}
	if err := runtime.Probe(ctx); err != nil {
		return fmt.Errorf("verify nft generation capability: %w", err)
	}
	if err := runtime.Stage(ctx, newTable, rules); err != nil {
		return fmt.Errorf("stage nft generation: %w", err)
	}
	if err := runtime.Switch(ctx, oldTable, newTable); err != nil {
		return fmt.Errorf("switch nft generation: %w", err)
	}
	return nil
}

// waitStableSnapshot waits for two consecutive successful, identical reads.
// It always imposes a two-second upper bound, even when the caller has none.
func waitStableSnapshot(ctx context.Context, runtime nftRuntime, table string) (map[flowKey]counters, error) {
	if runtime == nil {
		return nil, errors.New("wait for stable nft snapshot: nil runtime")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, stableSnapshotTimeout)
	defer cancel()

	var previous map[flowKey]counters
	var lastErr error
	for {
		current, err := runtime.ReadCounters(ctx, table)
		if err != nil {
			lastErr = err
			previous = nil
		} else {
			lastErr = nil
			if previous != nil && maps.Equal(previous, current) {
				return cloneCounters(current), nil
			}
			previous = cloneCounters(current)
		}

		timer := time.NewTimer(stableSnapshotInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if lastErr != nil {
				return nil, fmt.Errorf("stable nft counter snapshot timed out after read failure: %w", lastErr)
			}
			return nil, fmt.Errorf("stable nft counter snapshot timed out: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
