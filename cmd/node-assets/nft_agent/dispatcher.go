package main

import (
	"context"
	"errors"
	"sync"
)

// commandExecutor executes one already-parsed panel command and returns its
// response. The live implementation wraps the agent command switch; tests
// inject fakes.
type commandExecutor interface {
	Execute(ctx context.Context, cmd commandMessage) commandResponse
}

const (
	mutationLaneCapacity   = 16
	diagnosticLaneCapacity = 2
)

var errDispatcherClosed = errors.New("command dispatcher closed")

// isDiagnosticCommand reports whether a command belongs to the long-running
// diagnostic lane. Everything state-mutating stays in the ordered mutation
// lane so nft rule operations keep their dispatch order.
func isDiagnosticCommand(commandType string) bool {
	switch commandType {
	case "Iperf3Client", "Iperf3Server", "TcpPing":
		return true
	}
	return false
}

// commandDispatcher decouples the WebSocket reader from command execution.
// Two bounded lanes keep ordering guarantees without letting one slow
// diagnostic starve heartbeats or block nft mutations:
//
//   - mutations: capacity 16, strictly ordered, one worker.
//   - diagnostic: capacity 2, one worker, isolates iperf/tcpping.
//
// A full lane immediately answers a structured busy response carrying the
// original request ID instead of blocking the reader.
type commandDispatcher struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mutations  chan commandMessage
	diagnostic chan commandMessage
	executor   commandExecutor
	write      func(commandResponse) error
	wg         sync.WaitGroup
}

func newCommandDispatcher(parent context.Context, executor commandExecutor, write func(commandResponse) error) *commandDispatcher {
	ctx, cancel := context.WithCancel(parent)
	d := &commandDispatcher{
		ctx:        ctx,
		cancel:     cancel,
		mutations:  make(chan commandMessage, mutationLaneCapacity),
		diagnostic: make(chan commandMessage, diagnosticLaneCapacity),
		executor:   executor,
		write:      write,
	}
	d.wg.Add(2)
	go d.runLane(d.mutations)
	go d.runLane(d.diagnostic)
	return d
}

// Dispatch places one command on its lane without ever blocking the caller.
// Control messages (empty type, call) are ignored. A full lane answers busy.
func (d *commandDispatcher) Dispatch(cmd commandMessage) error {
	if cmd.Type == "" || cmd.Type == "call" {
		return nil
	}
	lane := d.mutations
	if isDiagnosticCommand(cmd.Type) {
		lane = d.diagnostic
	}
	// Closure check comes first: a buffered lane could still accept sends after
	// the workers exited, silently dropping the command.
	select {
	case <-d.ctx.Done():
		return errDispatcherClosed
	default:
	}
	select {
	case lane <- cmd:
		return nil
	default:
		_ = d.write(commandResponse{
			Type:      cmd.Type + "Response",
			Success:   false,
			Message:   "节点命令队列已满，请稍后重试",
			RequestID: cmd.RequestID,
		})
		return nil
	}
}

func (d *commandDispatcher) runLane(lane chan commandMessage) {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case cmd := <-lane:
			resp := d.executor.Execute(d.ctx, cmd)
			_ = d.write(resp)
		}
	}
}

// Close cancels both lanes and waits for the in-flight command (if any) in
// each worker to finish.
func (d *commandDispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}
