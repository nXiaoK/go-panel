package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

type fakeR2Store struct {
	headErr   error
	putErr    error
	listErr   error
	deleteErr error

	bucket        string
	putKey        string
	putData       []byte
	putSHA256     string
	objects       []r2StoredObject
	deleted       []string
	headCallCount int
	putCallCount  int
}

func (s *fakeR2Store) HeadBucket(_ context.Context, bucket string) error {
	s.headCallCount++
	s.bucket = bucket
	return s.headErr
}

func (s *fakeR2Store) PutFile(_ context.Context, bucket, key, filePath, sha256Hex string, size int64) error {
	s.putCallCount++
	s.bucket = bucket
	s.putKey = key
	s.putSHA256 = sha256Hex
	if s.putErr != nil {
		return s.putErr
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("fake store received incorrect size")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != sha256Hex {
		return errors.New("fake store received incorrect checksum")
	}
	s.putData = data
	s.objects = append(s.objects, r2StoredObject{Key: key, LastModified: time.Now().Add(time.Hour), Size: size})
	return nil
}

func (s *fakeR2Store) ListObjects(_ context.Context, _, prefix string) ([]r2StoredObject, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var filtered []r2StoredObject
	for _, object := range s.objects {
		if strings.HasPrefix(object.Key, prefix) {
			filtered = append(filtered, object)
		}
	}
	return filtered, nil
}

func (s *fakeR2Store) DeleteObject(_ context.Context, _, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, key)
	return nil
}

func setupR2BackupTest(t *testing.T) *fakeR2Store {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 测试密钥仅验证加解密流程；重复字符避免把测试夹具误认为可用凭据。
	ConfigureR2BackupRuntime(R2BackupRuntimeConfig{CredentialEncryptionKey: strings.Repeat("a", 32)})
	store := &fakeR2Store{}
	originalFactory := newR2Store
	newR2Store = func(r2ResolvedSettings) (r2ObjectStore, error) { return store, nil }
	t.Cleanup(func() {
		newR2Store = originalFactory
		ConfigureR2BackupRuntime(R2BackupRuntimeConfig{})
		_ = model.Close()
	})
	return store
}

func validR2SettingsUpdate() R2BackupSettingsUpdate {
	return R2BackupSettingsUpdate{
		Enabled:         true,
		AccountID:       "0123456789abcdef0123456789abcdef",
		AccessKeyID:     "r2-access-key-id",
		SecretAccessKey: "r2-secret-access-key-value",
		Bucket:          "flux-panel-backups",
		ObjectPrefix:    "production/panel-a",
		ScheduleTime:    "03:15",
		RetentionCount:  2,
	}
}

func TestR2SettingsEncryptSecretAndHideReservedConfig(t *testing.T) {
	setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: code=%d msg=%q", res.Code, res.Msg)
	}

	storedSecret := GetConfigValue(r2BackupSecretAccessKeyName)
	if !strings.HasPrefix(storedSecret, r2EncryptedSecretPrefix) {
		t.Fatalf("stored secret does not use encrypted envelope: %q", storedSecret)
	}
	if strings.Contains(storedSecret, request.SecretAccessKey) {
		t.Fatal("stored R2 credential contains plaintext secret")
	}
	viewRes := GetR2BackupSettings()
	if viewRes.Code != 0 {
		t.Fatalf("get R2 settings: %+v", viewRes)
	}
	view := viewRes.Data.(R2BackupSettings)
	if !view.Enabled || !view.SecretConfigured || !view.SecretUsable {
		t.Fatalf("unexpected credential view: %#v", view)
	}
	if view.AccountID != request.AccountID || view.Bucket != request.Bucket || view.RetentionCount != 2 {
		t.Fatalf("unexpected settings view: %#v", view)
	}

	adminConfigs := getConfigs(true).Data.(map[string]string)
	for name := range adminConfigs {
		if isR2BackupConfigName(name) {
			t.Fatalf("generic admin config leaked reserved R2 field %q", name)
		}
	}
	if res := UpdateConfig(r2BackupSecretAccessKeyName, "plaintext"); res.Code == 0 {
		t.Fatal("generic single config update accepted reserved R2 key")
	}
	if err := updateOrCreateConfig("app_name", "before"); err != nil {
		t.Fatalf("seed app name: %v", err)
	}
	if res := UpdateConfigs(map[string]string{
		"app_name":                  "must-not-save",
		r2BackupSecretAccessKeyName: "plaintext",
	}); res.Code == 0 {
		t.Fatal("generic batch config update accepted reserved R2 key")
	}
	if got := GetConfigValue("app_name"); got != "before" {
		t.Fatalf("reserved config rejection partially saved app_name=%q", got)
	}

	// 空密钥表示保留既有密文，避免设置页面读取后要求管理员重复输入。
	request.SecretAccessKey = ""
	request.ObjectPrefix = "production/panel-b"
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("update settings while retaining secret: %+v", res)
	}
	if got := GetConfigValue(r2BackupSecretAccessKeyName); got != storedSecret {
		t.Fatal("blank secret unexpectedly rotated or cleared stored credential")
	}
}

func TestR2SettingsRequirePersistentEncryptionKeyAndSafeClear(t *testing.T) {
	setupR2BackupTest(t)
	ConfigureR2BackupRuntime(R2BackupRuntimeConfig{})
	request := validR2SettingsUpdate()
	if res := UpdateR2BackupSettings(request); res.Code == 0 || !strings.Contains(res.Msg, "JWT_SECRET") {
		t.Fatalf("ephemeral encryption key accepted R2 secret: %+v", res)
	}

	ConfigureR2BackupRuntime(R2BackupRuntimeConfig{CredentialEncryptionKey: "first-persistent-key"})
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save encrypted secret: %+v", res)
	}
	ConfigureR2BackupRuntime(R2BackupRuntimeConfig{CredentialEncryptionKey: "different-persistent-key"})
	view := GetR2BackupSettings().Data.(R2BackupSettings)
	if !view.SecretConfigured || view.SecretUsable || !strings.Contains(view.CredentialMessage, "重新填写") {
		t.Fatalf("rotated encryption key was not reported safely: %#v", view)
	}

	request.Enabled = false
	request.SecretAccessKey = "replacement-secret"
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("replace undecryptable credential: %+v", res)
	}
	request.ClearSecret = true
	if res := UpdateR2BackupSettings(request); res.Code == 0 || !strings.Contains(res.Msg, "不能同时") {
		t.Fatalf("clear and replace secret were accepted together: %+v", res)
	}
	request.SecretAccessKey = ""
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("clear credential while disabled: %+v", res)
	}
	view = GetR2BackupSettings().Data.(R2BackupSettings)
	if view.SecretConfigured || view.SecretUsable {
		t.Fatalf("cleared secret still reported configured: %#v", view)
	}
	request.Enabled = true
	if res := UpdateR2BackupSettings(request); res.Code == 0 {
		t.Fatal("enabled automatic backup without a secret")
	}
}

func TestR2SettingsValidation(t *testing.T) {
	setupR2BackupTest(t)
	base := validR2SettingsUpdate()
	tests := []struct {
		name   string
		mutate func(*R2BackupSettingsUpdate)
		want   string
	}{
		{name: "account id", mutate: func(v *R2BackupSettingsUpdate) { v.AccountID = "not-an-account" }, want: "Account ID"},
		{name: "bucket", mutate: func(v *R2BackupSettingsUpdate) { v.Bucket = "Bad_Bucket" }, want: "存储桶"},
		{name: "prefix", mutate: func(v *R2BackupSettingsUpdate) { v.ObjectPrefix = "a/../b" }, want: "路径段"},
		{name: "time", mutate: func(v *R2BackupSettingsUpdate) { v.ScheduleTime = "25:00" }, want: "HH:MM"},
		{name: "retention", mutate: func(v *R2BackupSettingsUpdate) { v.RetentionCount = 366 }, want: "1-365"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			res := UpdateR2BackupSettings(request)
			if res.Code == 0 || !strings.Contains(res.Msg, test.want) {
				t.Fatalf("validation response=%+v, want %q", res, test.want)
			}
		})
	}
}

func TestRunR2BackupUploadsConsistentSnapshotAndPrunesManagedObjects(t *testing.T) {
	store := setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: %+v", res)
	}
	if err := updateOrCreateConfig("app_name", "R2 snapshot marker"); err != nil {
		t.Fatalf("seed snapshot marker: %v", err)
	}
	now := time.Now()
	managedPrefix := request.ObjectPrefix + "/flux-panel-backup-"
	oldest := managedPrefix + "20260801-010000.db"
	newer := managedPrefix + "20260802-010000.db"
	store.objects = []r2StoredObject{
		{Key: oldest, LastModified: now.Add(-2 * time.Hour)},
		{Key: newer, LastModified: now.Add(-time.Hour)},
		{Key: request.ObjectPrefix + "/notes.txt", LastModified: now.Add(-24 * time.Hour)},
		{Key: request.ObjectPrefix + "/flux-panel-backup-ignore.zip", LastModified: now.Add(-24 * time.Hour)},
	}

	res := RunR2BackupNow(context.Background())
	if res.Code != 0 {
		t.Fatalf("run R2 backup: code=%d msg=%q", res.Code, res.Msg)
	}
	summary := res.Data.(*R2BackupRunResult)
	if store.bucket != request.Bucket || store.putKey != summary.ObjectKey {
		t.Fatalf("upload target bucket=%q key=%q summary=%#v", store.bucket, store.putKey, summary)
	}
	if !bytes.HasPrefix(store.putData, []byte("SQLite format 3\x00")) {
		t.Fatal("uploaded object is not a SQLite backup")
	}
	if bytes.Contains(store.putData, []byte(request.SecretAccessKey)) {
		t.Fatal("uploaded database snapshot contains plaintext R2 secret")
	}
	if summary.Size != int64(len(store.putData)) || summary.SHA256 != store.putSHA256 || len(summary.SHA256) != 64 {
		t.Fatalf("unexpected upload integrity summary: %#v", summary)
	}
	if summary.DeletedObjects != 1 || len(store.deleted) != 1 || store.deleted[0] != oldest {
		t.Fatalf("retention deleted=%v summary=%#v", store.deleted, summary)
	}

	view := GetR2BackupSettings().Data.(R2BackupSettings)
	if view.LastSuccessAt == 0 || view.LastObjectKey != summary.ObjectKey || view.LastError != "" {
		t.Fatalf("success status was not persisted: %#v", view)
	}
	if view.LastSize != summary.Size || view.LastSHA256 != summary.SHA256 {
		t.Fatalf("integrity status mismatch: %#v", view)
	}
}

func TestR2ConnectionAndUploadFailuresAreReportedWithoutSecrets(t *testing.T) {
	store := setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: %+v", res)
	}
	store.headErr = errors.New("forbidden")
	if res := TestR2BackupConnection(context.Background()); res.Code == 0 || !strings.Contains(res.Msg, "forbidden") {
		t.Fatalf("connection failure response=%+v", res)
	}
	store.headErr = nil
	if res := TestR2BackupConnection(context.Background()); res.Code != 0 || store.headCallCount != 2 {
		t.Fatalf("connection success response=%+v calls=%d", res, store.headCallCount)
	}

	store.putErr = errors.New("simulated upload failure")
	res := RunR2BackupNow(context.Background())
	if res.Code == 0 || !strings.Contains(res.Msg, "simulated upload failure") {
		t.Fatalf("upload failure response=%+v", res)
	}
	if strings.Contains(res.Msg, request.SecretAccessKey) {
		t.Fatal("upload error leaked Secret Access Key")
	}
	view := GetR2BackupSettings().Data.(R2BackupSettings)
	if view.LastAttemptAt == 0 || !strings.Contains(view.LastError, "simulated upload failure") {
		t.Fatalf("failure status not persisted: %#v", view)
	}
}

func TestScheduledR2BackupCatchesUpDeduplicatesAndRetries(t *testing.T) {
	store := setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	request.ScheduleTime = "03:00"
	request.RetentionCount = 10
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: %+v", res)
	}
	location := time.FixedZone("panel-local", 8*60*60)
	before := time.Date(2026, 8, 2, 2, 59, 0, 0, location)
	if ran, _, err := RunScheduledR2Backup(context.Background(), before); err != nil || ran {
		t.Fatalf("backup ran before schedule: ran=%v err=%v", ran, err)
	}
	due := time.Date(2026, 8, 2, 3, 7, 0, 0, location)
	ran, summary, err := RunScheduledR2Backup(context.Background(), due)
	if err != nil || !ran || summary == nil {
		t.Fatalf("catch-up backup: ran=%v summary=%#v err=%v", ran, summary, err)
	}
	if ran, _, err := RunScheduledR2Backup(context.Background(), due.Add(time.Hour)); err != nil || ran {
		t.Fatalf("same-day backup was not deduplicated: ran=%v err=%v", ran, err)
	}

	store.putErr = errors.New("temporary R2 outage")
	nextDay := due.Add(24 * time.Hour)
	ran, _, err = RunScheduledR2Backup(context.Background(), nextDay)
	if err == nil || !ran {
		t.Fatalf("scheduled failure: ran=%v err=%v", ran, err)
	}
	putCallsAfterFailure := store.putCallCount
	if ran, _, err := RunScheduledR2Backup(context.Background(), nextDay.Add(5*time.Minute)); err != nil || ran {
		t.Fatalf("scheduled failure retried before backoff: ran=%v err=%v", ran, err)
	}
	if store.putCallCount != putCallsAfterFailure {
		t.Fatal("scheduled retry backoff still called object storage")
	}
	ran, _, err = RunScheduledR2Backup(context.Background(), nextDay.Add(16*time.Minute))
	if err == nil || !ran || store.putCallCount != putCallsAfterFailure+1 {
		t.Fatalf("scheduled retry after backoff: ran=%v calls=%d err=%v", ran, store.putCallCount, err)
	}
}

func TestScheduledR2BackupDoesNotReuploadAfterRetentionCleanupFailure(t *testing.T) {
	store := setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	request.ScheduleTime = "03:00"
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: %+v", res)
	}
	store.listErr = errors.New("missing list or delete permission")
	due := time.Date(2026, 8, 2, 3, 5, 0, 0, time.FixedZone("panel-local", 8*60*60))

	ran, summary, err := RunScheduledR2Backup(context.Background(), due)
	if !ran || summary == nil || err == nil || !strings.Contains(err.Error(), "已上传") {
		t.Fatalf("cleanup partial success: ran=%v summary=%#v err=%v", ran, summary, err)
	}
	if store.putCallCount != 1 {
		t.Fatalf("initial upload calls=%d", store.putCallCount)
	}
	view := GetR2BackupSettings().Data.(R2BackupSettings)
	if view.LastSuccessAt == 0 || view.LastObjectKey != summary.ObjectKey || !strings.Contains(view.LastError, "清理过期对象失败") {
		t.Fatalf("partial success status was not persisted: %#v", view)
	}
	if ran, _, err := RunScheduledR2Backup(context.Background(), due.Add(20*time.Minute)); ran || err != nil {
		t.Fatalf("same-day partial success was retried: ran=%v err=%v", ran, err)
	}
	if store.putCallCount != 1 {
		t.Fatalf("cleanup failure caused duplicate upload calls=%d", store.putCallCount)
	}
}

func TestScheduledR2BackupBacksOffInvalidScheduleConfiguration(t *testing.T) {
	setupR2BackupTest(t)
	request := validR2SettingsUpdate()
	if res := UpdateR2BackupSettings(request); res.Code != 0 {
		t.Fatalf("save R2 settings: %+v", res)
	}
	// 模拟旧版本、手工数据库编辑或恢复带来的损坏配置；专用更新接口不会接受此值。
	if err := writeR2ConfigValues(map[string]string{r2BackupScheduleTimeName: "invalid"}); err != nil {
		t.Fatalf("seed invalid schedule: %v", err)
	}
	now := time.Date(2026, 8, 2, 3, 5, 0, 0, time.Local)
	if ran, _, err := RunScheduledR2Backup(context.Background(), now); !ran || err == nil {
		t.Fatalf("invalid schedule was not reported: ran=%v err=%v", ran, err)
	}
	if ran, _, err := RunScheduledR2Backup(context.Background(), now.Add(5*time.Minute)); ran || err != nil {
		t.Fatalf("invalid schedule ignored retry backoff: ran=%v err=%v", ran, err)
	}
}

func TestPruneR2BackupsUsesKeyAsStableTieBreaker(t *testing.T) {
	store := &fakeR2Store{}
	modified := time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)
	store.objects = []r2StoredObject{
		{Key: "p/flux-panel-backup-20260802-030000.db", LastModified: modified},
		{Key: "p/flux-panel-backup-20260801-030000.db", LastModified: modified},
		{Key: "p/flux-panel-backup-20260731-030000.db", LastModified: modified},
		// 宽泛前后缀相同但不符合完整时间戳命名的手工对象不得参与轮转。
		{Key: "p/flux-panel-backup-manual.db", LastModified: modified.Add(-time.Hour)},
		{Key: "p/flux-panel-backup-20260802-030000-extra.db", LastModified: modified.Add(-time.Hour)},
		{Key: "p/flux-panel-backup-20260802-030000.db/nested.db", LastModified: modified.Add(-time.Hour)},
	}
	settings := r2ResolvedSettings{Bucket: "bucket", ObjectPrefix: "p", RetentionCount: 1}
	deleted, err := pruneR2Backups(context.Background(), store, settings)
	if err != nil || deleted != 2 {
		t.Fatalf("prune result deleted=%d err=%v", deleted, err)
	}
	sort.Strings(store.deleted)
	want := []string{
		"p/flux-panel-backup-20260731-030000.db",
		"p/flux-panel-backup-20260801-030000.db",
	}
	if strings.Join(store.deleted, "|") != strings.Join(want, "|") {
		t.Fatalf("deleted=%v want=%v", store.deleted, want)
	}
}
