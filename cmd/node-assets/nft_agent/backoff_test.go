package main

import (
	"strings"
	"testing"
	"time"
)

func TestBackoffCapsAtThirtySeconds(t *testing.T) {
	b := NewBackoff(func() float64 { return 0.5 })
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, expected := range want {
		if got := b.Next(); got != expected {
			t.Fatalf("step %d got=%v want=%v", i, got, expected)
		}
	}
}

func TestBackoffResetRestartsAtOneSecond(t *testing.T) {
	b := NewBackoff(func() float64 { return 0.5 })
	for i := 0; i < 5; i++ {
		b.Next()
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after reset got=%v want=1s", got)
	}
}

func TestBackoffAppliesJitterWithinTwentyPercent(t *testing.T) {
	low := NewBackoff(func() float64 { return 0 })
	if got := low.Next(); got != 800*time.Millisecond {
		t.Fatalf("low jitter got=%v want=800ms", got)
	}
	high := NewBackoff(func() float64 { return 1 })
	if got := high.Next(); got != 1200*time.Millisecond {
		t.Fatalf("high jitter got=%v want=1200ms", got)
	}
}

func TestScanIperf3OutputBoundsAccumulation(t *testing.T) {
	long := strings.Repeat("x", 4096)
	var input strings.Builder
	for i := 0; i < 1024; i++ {
		input.WriteString(long)
		input.WriteByte('\n')
	}
	var output strings.Builder
	lines := 0
	err := scanIperf3Output(strings.NewReader(input.String()), &output, func(string) { lines++ })
	if err != nil {
		t.Fatal(err)
	}
	if lines != 1024 {
		t.Fatalf("callback lines=%d, want all 1024 despite capture bound", lines)
	}
	if output.Len() > maxIperf3OutputBytes {
		t.Fatalf("captured %d bytes, want <= %d", output.Len(), maxIperf3OutputBytes)
	}
}
