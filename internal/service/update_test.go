package service

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/model"
)

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn updateRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func updateJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func preserveBuildInfo(t *testing.T) {
	t.Helper()
	version, commit, buildTime := buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime = version, commit, buildTime
		ConfigureUpdateRuntime(UpdateRuntimeConfig{
			Enabled:       true,
			Repository:    buildinfo.Repository,
			CheckInterval: 6 * time.Hour,
		})
	})
}

func TestCheckPanelUpdateUsesStableReleaseAndCache(t *testing.T) {
	preserveBuildInfo(t)
	buildinfo.Version = "v1.2.3"
	buildinfo.Commit = "abc1234"

	var requests atomic.Int32
	client := &http.Client{Transport: updateRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Path != "/repos/nXiaoK/go-panel/releases/latest" {
			t.Fatalf("unexpected release path %q", r.URL.Path)
		}
		return updateJSONResponse(http.StatusOK, `{
			"tag_name":"v1.3.0",
			"name":"Go Panel 1.3.0",
			"html_url":"https://github.com/nXiaoK/go-panel/releases/tag/v1.3.0",
			"body":"修复与增强",
			"published_at":"2026-08-10T00:00:00Z"
		}`), nil
	})}

	ConfigureUpdateRuntime(UpdateRuntimeConfig{
		Enabled:           true,
		Repository:        "nXiaoK/go-panel",
		CheckInterval:     time.Hour,
		ReleaseAPIBaseURL: "https://api.test",
		HTTPClient:        client,
	})
	first, err := checkPanelUpdate(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := checkPanelUpdate(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("release requests=%d, want 1 cached request", requests.Load())
	}
	if !first.UpdateAvailable || first.LatestVersion != "v1.3.0" || second.CheckedAt != first.CheckedAt {
		t.Fatalf("unexpected update status: first=%#v second=%#v", first, second)
	}
	if first.AutoUpdateConfigured {
		t.Fatal("automatic update must remain disabled without a trigger token")
	}
}

func TestCheckPanelUpdateDisabledDoesNotUseNetwork(t *testing.T) {
	preserveBuildInfo(t)
	ConfigureUpdateRuntime(UpdateRuntimeConfig{
		Enabled:           false,
		Repository:        "nXiaoK/go-panel",
		ReleaseAPIBaseURL: "http://127.0.0.1:1",
	})
	status, err := checkPanelUpdate(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.CheckedAt != 0 || status.UpdateAvailable {
		t.Fatalf("disabled status=%#v", status)
	}
}

func TestTriggerPanelUpdateBacksUpDatabaseAndCallsSidecar(t *testing.T) {
	preserveBuildInfo(t)
	buildinfo.Version = "v1.0.0"
	databasePath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(databasePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })

	var updateRequests atomic.Int32
	client := &http.Client{Transport: updateRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/nXiaoK/go-panel/releases/latest":
			return updateJSONResponse(http.StatusOK, `{
				"tag_name":"v1.1.0",
				"html_url":"https://github.com/nXiaoK/go-panel/releases/tag/v1.1.0"
			}`), nil
		case "/v1/update":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-update-token" {
				t.Fatalf("unexpected updater request method=%s auth=%q", r.Method, r.Header.Get("Authorization"))
			}
			updateRequests.Add(1)
			return updateJSONResponse(http.StatusNoContent, ""), nil
		default:
			return updateJSONResponse(http.StatusNotFound, ""), nil
		}
	})}

	ConfigureUpdateRuntime(UpdateRuntimeConfig{
		Enabled:           true,
		Repository:        "nXiaoK/go-panel",
		CheckInterval:     time.Hour,
		TriggerURL:        "http://updater.test/v1/update",
		TriggerToken:      "test-update-token",
		ReleaseAPIBaseURL: "https://api.test",
		HTTPClient:        client,
	})
	response := TriggerPanelUpdate(context.Background())
	if response.Code != 0 {
		t.Fatalf("TriggerPanelUpdate failed: %s", response.Msg)
	}
	data, ok := response.Data.(PanelUpdateTriggerResult)
	if !ok || !data.Started || data.TargetVersion != "v1.1.0" {
		t.Fatalf("unexpected trigger result %#v", response.Data)
	}
	if updateRequests.Load() != 1 {
		t.Fatalf("update requests=%d", updateRequests.Load())
	}
	backupPath := filepath.Join(filepath.Dir(databasePath), filepath.FromSlash(data.BackupFile))
	if info, err := os.Stat(backupPath); err != nil || info.Size() == 0 {
		t.Fatalf("persistent pre-update backup missing: path=%s err=%v", backupPath, err)
	}
}

func TestStableVersionComparison(t *testing.T) {
	current, ok := parseStableVersion("v2.9.10+build.1")
	if !ok {
		t.Fatal("current version did not parse")
	}
	latest, ok := parseStableVersion("2.10.0")
	if !ok || compareStableVersions(current, latest) >= 0 {
		t.Fatalf("comparison current=%v latest=%v ok=%v", current, latest, ok)
	}
	if _, ok := parseStableVersion("edge-abcdef0"); ok {
		t.Fatal("edge builds must not compare as stable releases")
	}
}

func TestAutoUpdateConfiguredRequiresLatestImageTag(t *testing.T) {
	base := UpdateRuntimeConfig{
		TriggerURL:   "http://updater:8080/v1/update",
		TriggerToken: "sidecar-secret",
	}
	if !autoUpdateConfigured(normalizeUpdateRuntimeConfig(base)) {
		t.Fatal("latest image tag should allow the configured update sidecar")
	}
	base.ImageTag = "v1.2.3"
	if autoUpdateConfigured(normalizeUpdateRuntimeConfig(base)) {
		t.Fatal("fixed image tag must disable cross-version one-click updates")
	}
}
