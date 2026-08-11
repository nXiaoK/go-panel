package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

func clearAgentSyncMarker() error {
	if err := os.Remove(agentSyncMarkerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear nft agent sync marker: %w", err)
	}
	return nil
}

func writeAgentSyncMarker() error {
	table, err := resolveActiveNftTableWith(context.Background(), nftgeneration.ActiveTableMarkerPath, runBoundedCommand)
	if err != nil {
		return fmt.Errorf("resolve synced nft table: %w", err)
	}
	if err := nftgeneration.WriteActiveMarker(agentSyncMarkerPath, table); err != nil {
		return fmt.Errorf("write nft agent sync marker: %w", err)
	}
	return nil
}
