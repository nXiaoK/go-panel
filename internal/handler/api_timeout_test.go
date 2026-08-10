package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineCaptureWriter struct {
	http.ResponseWriter
	deadline time.Time
}

func (w *deadlineCaptureWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestExtendNodeUpgradeWriteDeadline(t *testing.T) {
	w := &deadlineCaptureWriter{ResponseWriter: httptest.NewRecorder()}
	now := time.Unix(123, 0)
	if err := extendNodeUpgradeWriteDeadline(w, now); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(nodeUpgradeResponseTimeout); !w.deadline.Equal(want) {
		t.Fatalf("upgrade write deadline = %v, want %v", w.deadline, want)
	}
}
