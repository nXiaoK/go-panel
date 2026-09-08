package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-gost/core/observer"
	"github.com/go-gost/core/observer/stats"
	xstats "github.com/go-gost/x/observer/stats"
)

type unavailableTrafficObserver struct{}

func (unavailableTrafficObserver) Observe(context.Context, []observer.Event, ...observer.Option) error {
	return errors.New("observer offline")
}

func TestTrafficRetryContinuesWhenIdleAndObserverIsUnavailable(t *testing.T) {
	requests := make(chan TrafficReportItem, 4)
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var item TrafficReportItem
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		attempt++
		requests <- item
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	oldURL, oldCrypto := httpReportURL, httpAESCrypto
	httpReportURL, httpAESCrypto = server.URL, nil
	defer func() { httpReportURL, httpAESCrypto = oldURL, oldCrypto }()
	st := xstats.NewStats(false)
	st.Add(stats.KindInputBytes, 100)
	st.Add(stats.KindOutputBytes, 200)
	service := &defaultService{name: "1_2_3_tcp", status: &Status{stats: st}, options: options{observer: unavailableTrafficObserver{}, observerPeriod: time.Second}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.observeStats(ctx) }()
	defer func() { cancel(); <-done }()
	var sent []TrafficReportItem
	for len(sent) < 2 {
		select {
		case item := <-requests:
			sent = append(sent, item)
		case <-time.After(4 * time.Second):
			t.Fatal("idle traffic retry was blocked by observer state")
		}
	}
	if sent[0] != sent[1] || sent[0].U != 100 || sent[0].D != 200 {
		t.Fatalf("retried snapshot differs: %+v", sent)
	}
}

func TestTrafficReportKeepsDirectionsAndPendingSnapshot(t *testing.T) {
	st := xstats.NewStats(false)
	st.Add(stats.KindInputBytes, 100)
	st.Add(stats.KindOutputBytes, 200)
	var state trafficReportState
	var sent []TrafficReportItem
	send := func(_ context.Context, item TrafficReportItem) (bool, error) {
		sent = append(sent, item)
		if len(sent) == 1 {
			return false, errors.New("lost response after commit")
		}
		if len(sent) == 2 {
			// ACK 期间仍有连接流量，任何覆盖式清零都会存在丢失风险。
			st.Add(stats.KindInputBytes, 7)
			st.Add(stats.KindOutputBytes, 9)
		}
		return true, nil
	}
	if err := state.report(context.Background(), "1_2_3_tcp", st, send); err == nil {
		t.Fatal("expected transport failure")
	}
	st.Add(stats.KindInputBytes, 20)
	st.Add(stats.KindOutputBytes, 50)
	st.IsUpdated() // 观察器消耗更新标记，不应影响 HTTP 重试。
	for i := 0; i < 3; i++ {
		if err := state.report(context.Background(), "1_2_3_tcp", st, send); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 3 || sent[0] != sent[1] || sent[0].U != 100 || sent[0].D != 200 || sent[0].Sequence != 1 || sent[0].ReporterID == "" {
		t.Fatalf("pending snapshot or directions changed: %+v", sent)
	}
	if sent[2].U != 27 || sent[2].D != 59 || sent[2].Sequence != 2 || sent[2].ReporterID != sent[0].ReporterID {
		t.Fatalf("concurrent traffic lost or recounted: %+v", sent[2])
	}
	if st.Get(stats.KindInputBytes) != 127 || st.Get(stats.KindOutputBytes) != 259 {
		t.Fatal("reporting changed cumulative counters")
	}
}

func TestTrafficReportChunksLongOfflineBacklog(t *testing.T) {
	st := xstats.NewStats(false)
	st.Add(stats.KindInputBytes, int64(maxTrafficReportBytes+17))
	var state trafficReportState
	var sent []TrafficReportItem
	send := func(_ context.Context, item TrafficReportItem) (bool, error) {
		sent = append(sent, item)
		return true, nil
	}
	for i := 0; i < 3; i++ {
		if err := state.report(context.Background(), "1_2_3_tcp", st, send); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 2 || sent[0].U != int64(maxTrafficReportBytes) || sent[1].U != 17 {
		t.Fatalf("unbounded or incomplete backlog: %+v", sent)
	}
}
