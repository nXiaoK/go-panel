package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/service"
)

type deadlineCaptureWriter struct {
	http.ResponseWriter
	deadline     time.Time
	readDeadline time.Time
	readErr      error
	writeErr     error
}

func (w *deadlineCaptureWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.writeErr
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

func (w *deadlineCaptureWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return w.readErr
}

func TestExtendRequestDeadlines(t *testing.T) {
	now := time.Unix(123, 0)
	for _, tc := range []struct {
		name        string
		read, write time.Duration
	}{
		{"both", 2 * time.Minute, 3 * time.Minute},
		{"write only", 0, 3 * time.Minute},
		{"read only", 2 * time.Minute, 0},
		{"unchanged", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &deadlineCaptureWriter{ResponseWriter: httptest.NewRecorder()}
			if err := extendRequestDeadlines(w, now, tc.read, tc.write); err != nil {
				t.Fatal(err)
			}
			wantRead, wantWrite := time.Time{}, time.Time{}
			if tc.read > 0 {
				wantRead = now.Add(tc.read)
			}
			if tc.write > 0 {
				wantWrite = now.Add(tc.write)
			}
			if !w.readDeadline.Equal(wantRead) || !w.deadline.Equal(wantWrite) {
				t.Fatalf("deadlines read=%v write=%v, want read=%v write=%v", w.readDeadline, w.deadline, wantRead, wantWrite)
			}
		})
	}
	t.Run("unsupported", func(t *testing.T) {
		err := extendRequestDeadlines(httptest.NewRecorder(), now, time.Minute, time.Minute)
		if !errors.Is(err, http.ErrNotSupported) {
			t.Fatalf("error=%v, want ErrNotSupported", err)
		}
	})
	t.Run("both attempted after read failure", func(t *testing.T) {
		readErr, writeErr := errors.New("read failed"), errors.New("write failed")
		w := &deadlineCaptureWriter{ResponseWriter: httptest.NewRecorder(), readErr: readErr, writeErr: writeErr}
		err := extendRequestDeadlines(w, now, time.Minute, 2*time.Minute)
		if !errors.Is(err, readErr) || !errors.Is(err, writeErr) || !w.deadline.Equal(now.Add(2*time.Minute)) {
			t.Fatalf("deadlines not both attempted or errors lost: %+v, %v", w, err)
		}
	})
}

func TestExtendRequestDeadlinesRealHTTP(t *testing.T) {
	for _, extend := range []bool{false, true} {
		name := "global timeout disconnects"
		if extend {
			name = "request deadline allows response"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if extend {
					if err := extendRequestDeadlines(w, time.Now(), 0, 3*time.Second); err != nil {
						t.Errorf("extend real write deadline: %v", err)
						return
					}
				}
				time.Sleep(1500 * time.Millisecond)
				_, _ = io.WriteString(w, "complete response")
			}))
			server.Config.WriteTimeout = time.Second
			server.Start()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			response, err := client.Get(server.URL)
			if !extend {
				if err == nil {
					response.Body.Close()
					t.Fatal("expected global write timeout to close connection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "complete response" {
				t.Fatalf("response=%q error=%v", body, err)
			}
		})
	}
}

func TestDiagnosisDeadlineExtenderUsesWorstCase(t *testing.T) {
	w := &deadlineCaptureWriter{ResponseWriter: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(w)
	before := time.Now()
	diagnosisDeadlineExtender(c, "转发")(40 * time.Second)
	want := service.DiagnosisResponseTimeout(40 * time.Second)
	if got := w.deadline.Sub(before); got < want || got > want+time.Second {
		t.Fatalf("write deadline offset=%v, want about %v", got, want)
	}
	diagnosisDeadlineExtender(c, "转发")(time.Hour)
	if got := w.deadline.Sub(before); got > 5*time.Minute+time.Second {
		t.Fatalf("deadline not clamped: %v", got)
	}
}
