package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)
type nftStdinRunner func(context.Context, string, []string, string) ([]byte, error)

type boundedCommandBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedCommandBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		write := len(p)
		if write > remaining {
			write = remaining
		}
		_, _ = b.buffer.Write(p[:write])
	}
	if len(p) > remaining {
		b.exceeded = true
	}
	return len(p), nil
}

func (b *boundedCommandBuffer) result() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buffer.Bytes()), b.exceeded
}

func runBoundedCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return runBoundedExec(cmd)
}

func runBoundedNftStdin(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	return runBoundedExec(cmd)
}

func runBoundedExec(cmd *exec.Cmd) ([]byte, error) {
	output := &boundedCommandBuffer{limit: maxNftCommandOutput}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	raw, exceeded := output.result()
	if exceeded {
		return raw, fmt.Errorf("command output exceeds %d bytes", maxNftCommandOutput)
	}
	return raw, err
}

func applyNftRulesWithRunner(scriptPath string, run commandRunner) error {
	if scriptPath == "" || run == nil {
		return errors.New("apply nft rules: incomplete runner configuration")
	}
	// The script runs a crash-recoverable, multi-transaction reporter handoff.
	// A single nft command timeout must not cancel the whole state machine.
	ctx := context.Background()
	output, err := run(ctx, scriptPath)
	if err != nil {
		return fmt.Errorf("apply nft rules: %w, output: %s", err, string(output))
	}
	return nil
}

// writeMu 串行化对 WS 连接的写操作。gorilla/websocket 要求同一连接的并发写必须串行，
// 否则可能触发 panic 或数据损坏。心跳 goroutine（sendSystemInfo）与 reader goroutine
// （sendResponse）都会写，必须经此锁保护。
var writeMu sync.Mutex
