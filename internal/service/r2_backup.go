package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"gorm.io/gorm"

	panelcrypto "github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	// backup_r2_ 是 R2 专用内部命名空间；通用配置接口会隐藏并拒绝修改这些键，
	// 防止密文凭据和调度状态被“高级配置”误读或覆盖。
	r2BackupConfigPrefix = "backup_r2_"

	// 用户设置：自动任务默认关闭；账号只能生成 Cloudflare 官方 HTTPS 端点。
	// Access Key ID 可回传给管理员，Secret Access Key 只保存带版本前缀的密文。
	r2BackupEnabledName         = r2BackupConfigPrefix + "enabled"
	r2BackupAccountIDName       = r2BackupConfigPrefix + "account_id"
	r2BackupAccessKeyIDName     = r2BackupConfigPrefix + "access_key_id"
	r2BackupSecretAccessKeyName = r2BackupConfigPrefix + "secret_access_key"
	// 存储位置和调度：存储桶必须保持私有；对象前缀用于隔离面板并限定轮转范围；
	// 每日时间按服务器本地时区解释；保留数量只允许 1-365。
	r2BackupBucketName         = r2BackupConfigPrefix + "bucket"
	r2BackupObjectPrefixName   = r2BackupConfigPrefix + "object_prefix"
	r2BackupScheduleTimeName   = r2BackupConfigPrefix + "schedule_time"
	r2BackupRetentionCountName = r2BackupConfigPrefix + "retention_count"
	// 运行状态也持久化到专用命名空间，用于重启后补跑、同日去重、失败退避，
	// 以及向管理员展示最近对象、大小、校验值和错误；这些字段不可手工配置。
	r2BackupLastAttemptAtName        = r2BackupConfigPrefix + "last_attempt_at"
	r2BackupLastSuccessAtName        = r2BackupConfigPrefix + "last_success_at"
	r2BackupLastObjectKeyName        = r2BackupConfigPrefix + "last_object_key"
	r2BackupLastErrorName            = r2BackupConfigPrefix + "last_error"
	r2BackupLastSizeName             = r2BackupConfigPrefix + "last_size"
	r2BackupLastSHA256Name           = r2BackupConfigPrefix + "last_sha256"
	r2BackupLastScheduledDateName    = r2BackupConfigPrefix + "last_scheduled_date"
	r2BackupLastScheduledAttemptName = r2BackupConfigPrefix + "last_scheduled_attempt_at"

	// 安全默认值：不开启时也向设置页展示这些值；计划时间使用服务器本地时区。
	defaultR2BackupObjectPrefix   = "flux-panel/backups"
	defaultR2BackupScheduleTime   = "03:00"
	defaultR2BackupRetentionCount = 30
	// 密文版本前缀用于拒绝历史明文或未知格式；失败任务每 15 分钟重试，
	// 错误状态按 Unicode 字符截断，避免远端响应无限占用数据库。
	r2EncryptedSecretPrefix  = "aesgcm:v1:"
	r2ScheduledRetryInterval = 15 * time.Minute
	r2StatusErrorMaxLength   = 1000
)

var (
	r2AccountIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	r2BucketPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	r2SchedulePattern  = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

	r2BackupRunMu sync.Mutex
	r2RuntimeMu   sync.RWMutex
	r2Runtime     R2BackupRuntimeConfig

	newR2Store = newAWSR2ObjectStore
)

// R2BackupRuntimeConfig 保存不能落入数据库的运行时安全材料。
// CredentialEncryptionKey 必须来自持久的 JWT_SECRET；为空时禁止保存 R2 Secret Access Key，
// 避免开发环境临时 JWT 密钥在重启后导致已保存凭据永久无法解密。
type R2BackupRuntimeConfig struct {
	CredentialEncryptionKey string
}

// ConfigureR2BackupRuntime 在服务启动时注入持久凭据加密密钥。
func ConfigureR2BackupRuntime(config R2BackupRuntimeConfig) {
	r2RuntimeMu.Lock()
	r2Runtime = config
	r2RuntimeMu.Unlock()
}

// R2BackupSettings 是管理员可读取的安全视图；Secret Access Key 永不返回前端。
type R2BackupSettings struct {
	// 自动备份默认关闭；开启时凭据、账号和存储桶必须全部有效。
	Enabled bool `json:"enabled"`
	// AccountID 仅允许 32 位十六进制，用于拼接官方 R2 端点，不接受自定义地址。
	AccountID string `json:"accountId"`
	// AccessKeyID 不是密钥正文，但只通过管理员专用接口返回。
	AccessKeyID string `json:"accessKeyId"`
	// Bucket 应为私有桶；ObjectPrefix 用于隔离多面板并限制远端删除范围。
	Bucket       string `json:"bucket"`
	ObjectPrefix string `json:"objectPrefix"`
	// ScheduleTime 为服务器本地时区的 HH:MM；RetentionCount 范围 1-365。
	ScheduleTime   string `json:"scheduleTime"`
	RetentionCount int    `json:"retentionCount"`
	// 凭据状态只暴露“是否配置/可用”，Secret Access Key 明文和密文均不回传。
	SecretConfigured              bool   `json:"secretConfigured"`
	SecretUsable                  bool   `json:"secretUsable"`
	CredentialEncryptionAvailable bool   `json:"credentialEncryptionAvailable"`
	CredentialMessage             string `json:"credentialMessage,omitempty"`
	LastAttemptAt                 int64  `json:"lastAttemptAt"`
	LastSuccessAt                 int64  `json:"lastSuccessAt"`
	LastObjectKey                 string `json:"lastObjectKey,omitempty"`
	LastError                     string `json:"lastError,omitempty"`
	LastSize                      int64  `json:"lastSize"`
	LastSHA256                    string `json:"lastSha256,omitempty"`
}

// R2BackupSettingsUpdate 更新自动备份设置。SecretAccessKey 为空时保留原密钥；
// ClearSecret=true 会清除密钥，不能同时提交新密钥，且只能在关闭自动备份时使用。
type R2BackupSettingsUpdate struct {
	// Enabled 默认 false；开启前后端会强制解析并验证完整凭据。
	Enabled bool `json:"enabled"`
	// AccountID、Bucket 和 ObjectPrefix 共同决定唯一的官方远端目标及轮转范围。
	AccountID   string `json:"accountId"`
	AccessKeyID string `json:"accessKeyId"`
	// SecretAccessKey 为空表示保留旧密文；ClearSecret 与新密钥互斥，且开启时不能清除。
	SecretAccessKey string `json:"secretAccessKey"`
	ClearSecret     bool   `json:"clearSecret"`
	Bucket          string `json:"bucket"`
	ObjectPrefix    string `json:"objectPrefix"`
	// ScheduleTime 使用服务器本地 HH:MM；RetentionCount 只允许 1-365，0 会恢复默认 30。
	ScheduleTime   string `json:"scheduleTime"`
	RetentionCount int    `json:"retentionCount"`
}

// R2BackupRunResult 返回一次成功上传及远端保留清理的摘要。
type R2BackupRunResult struct {
	ObjectKey      string `json:"objectKey"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	DeletedObjects int    `json:"deletedObjects"`
	CompletedAt    int64  `json:"completedAt"`
}

type r2StoredSettings struct {
	R2BackupSettings
	encryptedSecret      string
	lastScheduledDate    string
	lastScheduledAttempt int64
}

type r2ResolvedSettings struct {
	Enabled         bool
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	ObjectPrefix    string
	ScheduleTime    string
	RetentionCount  int
}

func isR2BackupConfigName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), r2BackupConfigPrefix)
}

func currentR2EncryptionKey() string {
	r2RuntimeMu.RLock()
	key := r2Runtime.CredentialEncryptionKey
	r2RuntimeMu.RUnlock()
	return key
}

func encryptR2Secret(secret string) (string, error) {
	key := currentR2EncryptionKey()
	if key == "" {
		return "", errors.New("保存 R2 密钥前必须配置持久 JWT_SECRET 并重启面板")
	}
	cipher, err := panelcrypto.NewAESCrypto(key)
	if err != nil {
		return "", fmt.Errorf("初始化 R2 凭据加密失败: %w", err)
	}
	encrypted, err := cipher.EncryptString(secret)
	if err != nil {
		return "", fmt.Errorf("加密 R2 凭据失败: %w", err)
	}
	return r2EncryptedSecretPrefix + encrypted, nil
}

func decryptR2Secret(encrypted string) (string, error) {
	if encrypted == "" {
		return "", errors.New("尚未配置 R2 Secret Access Key")
	}
	if !strings.HasPrefix(encrypted, r2EncryptedSecretPrefix) {
		return "", errors.New("R2 密钥存储格式无效，请重新填写")
	}
	key := currentR2EncryptionKey()
	if key == "" {
		return "", errors.New("缺少持久 JWT_SECRET，无法解密 R2 密钥")
	}
	cipher, err := panelcrypto.NewAESCrypto(key)
	if err != nil {
		return "", fmt.Errorf("初始化 R2 凭据解密失败: %w", err)
	}
	plain, err := cipher.DecryptString(strings.TrimPrefix(encrypted, r2EncryptedSecretPrefix))
	if err != nil {
		return "", errors.New("R2 密钥无法解密，请确认 JWT_SECRET 未变更并重新填写密钥")
	}
	return plain, nil
}

func loadR2StoredSettings() (r2StoredSettings, error) {
	var rows []model.ViteConfig
	if err := model.DB.Where("name LIKE ?", r2BackupConfigPrefix+"%").Find(&rows).Error; err != nil {
		return r2StoredSettings{}, fmt.Errorf("读取 R2 备份设置失败: %w", err)
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Name] = row.Value
	}

	settings := r2StoredSettings{}
	settings.Enabled = strings.EqualFold(strings.TrimSpace(values[r2BackupEnabledName]), "true")
	settings.AccountID = strings.TrimSpace(values[r2BackupAccountIDName])
	settings.AccessKeyID = strings.TrimSpace(values[r2BackupAccessKeyIDName])
	settings.Bucket = strings.TrimSpace(values[r2BackupBucketName])
	settings.ObjectPrefix = strings.Trim(strings.TrimSpace(values[r2BackupObjectPrefixName]), "/")
	if settings.ObjectPrefix == "" {
		settings.ObjectPrefix = defaultR2BackupObjectPrefix
	}
	settings.ScheduleTime = strings.TrimSpace(values[r2BackupScheduleTimeName])
	if settings.ScheduleTime == "" {
		settings.ScheduleTime = defaultR2BackupScheduleTime
	}
	settings.RetentionCount = parsePositiveInt(values[r2BackupRetentionCountName], defaultR2BackupRetentionCount)
	settings.encryptedSecret = values[r2BackupSecretAccessKeyName]
	settings.SecretConfigured = settings.encryptedSecret != ""
	settings.CredentialEncryptionAvailable = currentR2EncryptionKey() != ""
	settings.LastAttemptAt = parseInt64(values[r2BackupLastAttemptAtName])
	settings.LastSuccessAt = parseInt64(values[r2BackupLastSuccessAtName])
	settings.LastObjectKey = values[r2BackupLastObjectKeyName]
	settings.LastError = values[r2BackupLastErrorName]
	settings.LastSize = parseInt64(values[r2BackupLastSizeName])
	settings.LastSHA256 = values[r2BackupLastSHA256Name]
	settings.lastScheduledDate = values[r2BackupLastScheduledDateName]
	settings.lastScheduledAttempt = parseInt64(values[r2BackupLastScheduledAttemptName])

	if !settings.SecretConfigured {
		settings.CredentialMessage = "尚未配置 Secret Access Key"
	} else if _, err := decryptR2Secret(settings.encryptedSecret); err != nil {
		settings.CredentialMessage = err.Error()
	} else {
		settings.SecretUsable = true
	}
	return settings, nil
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseInt64(raw string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value
}

// GetR2BackupSettings 返回脱敏设置和最近运行状态。
func GetR2BackupSettings() result.R {
	settings, err := loadR2StoredSettings()
	if err != nil {
		return result.Err(err.Error())
	}
	return result.Ok(settings.R2BackupSettings)
}

// UpdateR2BackupSettings 原子保存 R2 配置；密钥只保存 AES-GCM 密文且永不回传。
func UpdateR2BackupSettings(request R2BackupSettingsUpdate) result.R {
	current, err := loadR2StoredSettings()
	if err != nil {
		return result.Err(err.Error())
	}

	request.AccountID = strings.ToLower(strings.TrimSpace(request.AccountID))
	request.AccessKeyID = strings.TrimSpace(request.AccessKeyID)
	request.SecretAccessKey = strings.TrimSpace(request.SecretAccessKey)
	request.Bucket = strings.ToLower(strings.TrimSpace(request.Bucket))
	request.ObjectPrefix = strings.Trim(strings.TrimSpace(request.ObjectPrefix), "/")
	request.ScheduleTime = strings.TrimSpace(request.ScheduleTime)
	if request.ClearSecret && request.SecretAccessKey != "" {
		return result.Err("清除 R2 密钥时不能同时提交新的 Secret Access Key")
	}
	if request.ObjectPrefix == "" {
		request.ObjectPrefix = defaultR2BackupObjectPrefix
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = defaultR2BackupScheduleTime
	}
	if request.RetentionCount == 0 {
		request.RetentionCount = defaultR2BackupRetentionCount
	}
	if err := validateR2PublicSettings(request.AccountID, request.AccessKeyID, request.Bucket, request.ObjectPrefix, request.ScheduleTime, request.RetentionCount); err != nil {
		return result.Err(err.Error())
	}

	encryptedSecret := current.encryptedSecret
	if request.ClearSecret {
		encryptedSecret = ""
	}
	if request.SecretAccessKey != "" {
		if len(request.SecretAccessKey) > 512 {
			return result.Err("R2 Secret Access Key 长度不能超过 512 个字符")
		}
		encryptedSecret, err = encryptR2Secret(request.SecretAccessKey)
		if err != nil {
			return result.Err(err.Error())
		}
	}
	if request.Enabled {
		if _, err := resolveR2SettingsValues(request.AccountID, request.AccessKeyID, encryptedSecret, request.Bucket, request.ObjectPrefix, request.ScheduleTime, request.RetentionCount); err != nil {
			return result.Err("无法开启 R2 自动备份：" + err.Error())
		}
	}

	values := map[string]string{
		r2BackupEnabledName:         strconv.FormatBool(request.Enabled),
		r2BackupAccountIDName:       request.AccountID,
		r2BackupAccessKeyIDName:     request.AccessKeyID,
		r2BackupSecretAccessKeyName: encryptedSecret,
		r2BackupBucketName:          request.Bucket,
		r2BackupObjectPrefixName:    request.ObjectPrefix,
		r2BackupScheduleTimeName:    request.ScheduleTime,
		r2BackupRetentionCountName:  strconv.Itoa(request.RetentionCount),
	}
	if err := writeR2ConfigValues(values); err != nil {
		return result.Err("保存 R2 备份设置失败")
	}
	updated, err := loadR2StoredSettings()
	if err != nil {
		return result.Err(err.Error())
	}
	return result.Ok(updated.R2BackupSettings)
}

func validateR2PublicSettings(accountID, accessKeyID, bucket, prefix, scheduleTime string, retention int) error {
	if accountID != "" && !r2AccountIDPattern.MatchString(accountID) {
		return errors.New("Cloudflare Account ID 必须是 32 位十六进制字符串")
	}
	if len(accessKeyID) > 256 || containsControl(accessKeyID) {
		return errors.New("R2 Access Key ID 格式无效")
	}
	if bucket != "" && !r2BucketPattern.MatchString(bucket) {
		return errors.New("R2 存储桶名称必须为 3-63 位小写字母、数字或连字符，并以字母或数字开头和结尾")
	}
	if len(prefix) > 512 || containsControl(prefix) || strings.Contains(prefix, "\\") {
		return errors.New("R2 对象前缀不能超过 512 个字符，且不能包含控制字符或反斜杠")
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "." || segment == ".." {
			return errors.New("R2 对象前缀不能包含 . 或 .. 路径段")
		}
	}
	if !r2SchedulePattern.MatchString(scheduleTime) {
		return errors.New("R2 自动备份时间必须使用 HH:MM（00:00-23:59）格式")
	}
	if retention < 1 || retention > 365 {
		return errors.New("R2 备份保留数量必须在 1-365 之间")
	}
	return nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func resolveR2SettingsValues(accountID, accessKeyID, encryptedSecret, bucket, prefix, scheduleTime string, retention int) (r2ResolvedSettings, error) {
	if err := validateR2PublicSettings(accountID, accessKeyID, bucket, prefix, scheduleTime, retention); err != nil {
		return r2ResolvedSettings{}, err
	}
	if accountID == "" {
		return r2ResolvedSettings{}, errors.New("请填写 Cloudflare Account ID")
	}
	if accessKeyID == "" {
		return r2ResolvedSettings{}, errors.New("请填写 R2 Access Key ID")
	}
	if bucket == "" {
		return r2ResolvedSettings{}, errors.New("请填写 R2 存储桶名称")
	}
	secret, err := decryptR2Secret(encryptedSecret)
	if err != nil {
		return r2ResolvedSettings{}, err
	}
	return r2ResolvedSettings{
		AccountID:       accountID,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secret,
		Bucket:          bucket,
		ObjectPrefix:    prefix,
		ScheduleTime:    scheduleTime,
		RetentionCount:  retention,
	}, nil
}

func resolveR2Settings(requireEnabled bool) (r2ResolvedSettings, r2StoredSettings, error) {
	stored, err := loadR2StoredSettings()
	if err != nil {
		return r2ResolvedSettings{}, stored, err
	}
	if requireEnabled && !stored.Enabled {
		return r2ResolvedSettings{}, stored, errors.New("R2 自动备份尚未开启")
	}
	resolved, err := resolveR2SettingsValues(
		stored.AccountID,
		stored.AccessKeyID,
		stored.encryptedSecret,
		stored.Bucket,
		stored.ObjectPrefix,
		stored.ScheduleTime,
		stored.RetentionCount,
	)
	if err != nil {
		return r2ResolvedSettings{}, stored, err
	}
	resolved.Enabled = stored.Enabled
	return resolved, stored, nil
}

func writeR2ConfigValues(values map[string]string) error {
	names := make([]string, 0, len(values))
	for name := range values {
		if !isR2BackupConfigName(name) {
			return fmt.Errorf("非 R2 配置键: %s", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	now := time.Now().UnixMilli()
	return model.DB.Transaction(func(tx *gorm.DB) error {
		for _, name := range names {
			if err := upsertConfig(tx, name, values[name], now); err != nil {
				return err
			}
		}
		return nil
	})
}

// TestR2BackupConnection 使用当前已保存且可解密的凭据检查存储桶访问权限。
func TestR2BackupConnection(ctx context.Context) result.R {
	settings, _, err := resolveR2Settings(false)
	if err != nil {
		return result.Err(err.Error())
	}
	store, err := newR2Store(settings)
	if err != nil {
		return result.Err("创建 R2 客户端失败：" + err.Error())
	}
	if err := store.HeadBucket(ctx, settings.Bucket); err != nil {
		return result.Err("R2 连接测试失败：" + err.Error())
	}
	return result.OkMsg("R2 连接成功，存储桶可访问")
}

// RunR2BackupNow 立即生成一致 SQLite 快照并上传，不要求自动备份开关已开启。
func RunR2BackupNow(ctx context.Context) result.R {
	summary, err := runR2Backup(ctx, time.Now(), "", false)
	if err != nil {
		return result.Err(err.Error())
	}
	return result.Ok(summary)
}

// RunScheduledR2Backup 每分钟由调度器调用。到达服务器本地计划时间后补跑，
// 成功日期持久化以避免重启重复；失败最多每 15 分钟重试一次，避免持续请求 R2。
func RunScheduledR2Backup(ctx context.Context, now time.Time) (bool, *R2BackupRunResult, error) {
	stored, err := loadR2StoredSettings()
	if err != nil {
		return false, nil, err
	}
	if !stored.Enabled {
		return false, nil, nil
	}
	date := now.Format("2006-01-02")
	if stored.lastScheduledDate == date {
		return false, nil, nil
	}
	// 无效时间等配置错误也遵守失败退避，避免损坏/旧版配置每分钟刷库和日志。
	if stored.lastScheduledAttempt > 0 {
		lastAttempt := time.UnixMilli(stored.lastScheduledAttempt)
		if delta := now.Sub(lastAttempt); delta >= 0 && delta < r2ScheduledRetryInterval {
			return false, nil, nil
		}
	}
	scheduledMinute, err := parseScheduleMinute(stored.ScheduleTime)
	if err != nil {
		return true, nil, recordR2FailureError(now, date, err)
	}
	currentMinute := now.Hour()*60 + now.Minute()
	if currentMinute < scheduledMinute {
		return false, nil, nil
	}
	summary, err := runR2Backup(ctx, now, date, true)
	return true, summary, err
}

func parseScheduleMinute(value string) (int, error) {
	if !r2SchedulePattern.MatchString(value) {
		return 0, errors.New("R2 自动备份时间配置无效")
	}
	parts := strings.Split(value, ":")
	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])
	return hour*60 + minute, nil
}

func runR2Backup(ctx context.Context, now time.Time, scheduledDate string, requireEnabled bool) (*R2BackupRunResult, error) {
	if !r2BackupRunMu.TryLock() {
		return nil, errors.New("已有 R2 备份任务正在运行")
	}
	defer r2BackupRunMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := recordR2BackupAttempt(now, scheduledDate); err != nil {
		return nil, fmt.Errorf("记录 R2 备份尝试失败: %w", err)
	}
	settings, _, err := resolveR2Settings(requireEnabled)
	if err != nil {
		return nil, recordR2FailureError(now, scheduledDate, err)
	}
	store, err := newR2Store(settings)
	if err != nil {
		err = fmt.Errorf("创建 R2 客户端失败: %w", err)
		return nil, recordR2FailureError(now, scheduledDate, err)
	}

	backup, err := CreateSiteBackup()
	if err != nil {
		err = fmt.Errorf("创建站点备份失败: %w", err)
		return nil, recordR2FailureError(now, scheduledDate, err)
	}
	defer backup.Cleanup()

	sha256Hex, size, err := fileSHA256(backup.Path)
	if err != nil {
		err = fmt.Errorf("计算备份校验值失败: %w", err)
		return nil, recordR2FailureError(now, scheduledDate, err)
	}
	filename := "flux-panel-backup-" + now.Format("20060102-150405") + ".db"
	objectKey := settings.ObjectPrefix + "/" + filename
	if err := store.PutFile(ctx, settings.Bucket, objectKey, backup.Path, sha256Hex, size); err != nil {
		err = fmt.Errorf("上传备份到 R2 失败: %w", err)
		return nil, recordR2FailureError(now, scheduledDate, err)
	}
	completedAt := time.Now().UnixMilli()
	summary := &R2BackupRunResult{
		ObjectKey:   objectKey,
		Size:        size,
		SHA256:      sha256Hex,
		CompletedAt: completedAt,
	}
	deleted, err := pruneR2Backups(ctx, store, settings)
	summary.DeletedObjects = deleted
	if err != nil {
		err = fmt.Errorf("R2 备份对象 %s 已上传，但清理过期对象失败: %w", objectKey, err)
		// 上传已经成功时持久化成功日期和对象摘要，避免调度器因清理权限不足
		// 每 15 分钟重复上传并无限堆积；错误保留给管理员，次日备份会再次清理。
		if statusErr := recordR2BackupSuccess(summary, scheduledDate, err.Error()); statusErr != nil {
			return summary, errors.Join(err, fmt.Errorf("保存 R2 部分成功状态失败: %w", statusErr))
		}
		return summary, err
	}
	if err := recordR2BackupSuccess(summary, scheduledDate, ""); err != nil {
		return nil, fmt.Errorf("R2 备份已上传，但保存运行状态失败: %w", err)
	}
	return summary, nil
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func pruneR2Backups(ctx context.Context, store r2ObjectStore, settings r2ResolvedSettings) (int, error) {
	managedPrefix := settings.ObjectPrefix + "/flux-panel-backup-"
	// 只轮转本功能使用的完整时间戳命名，避免仅凭宽泛前后缀删除
	// 同一对象前缀下由管理员手工存放的其他 .db 文件。
	managedNamePattern := regexp.MustCompile(`^` + regexp.QuoteMeta(managedPrefix) + `\d{8}-\d{6}\.db$`)
	objects, err := store.ListObjects(ctx, settings.Bucket, managedPrefix)
	if err != nil {
		return 0, err
	}
	managed := make([]r2StoredObject, 0, len(objects))
	for _, object := range objects {
		if managedNamePattern.MatchString(object.Key) {
			managed = append(managed, object)
		}
	}
	sort.Slice(managed, func(i, j int) bool {
		if !managed[i].LastModified.Equal(managed[j].LastModified) {
			return managed[i].LastModified.After(managed[j].LastModified)
		}
		return managed[i].Key > managed[j].Key
	})
	deleted := 0
	for _, object := range managed[min(settings.RetentionCount, len(managed)):] {
		if err := store.DeleteObject(ctx, settings.Bucket, object.Key); err != nil {
			return deleted, fmt.Errorf("删除 %s 失败: %w", object.Key, err)
		}
		deleted++
	}
	return deleted, nil
}

func recordR2BackupAttempt(now time.Time, scheduledDate string) error {
	values := map[string]string{
		r2BackupLastAttemptAtName: strconv.FormatInt(now.UnixMilli(), 10),
	}
	if scheduledDate != "" {
		values[r2BackupLastScheduledAttemptName] = strconv.FormatInt(now.UnixMilli(), 10)
	}
	return writeR2ConfigValues(values)
}

func recordR2FailureError(now time.Time, scheduledDate string, runErr error) error {
	if statusErr := recordR2BackupFailure(now, scheduledDate, runErr); statusErr != nil {
		return errors.Join(runErr, fmt.Errorf("保存 R2 失败状态失败: %w", statusErr))
	}
	return runErr
}

func recordR2BackupFailure(now time.Time, scheduledDate string, runErr error) error {
	values := map[string]string{
		r2BackupLastAttemptAtName: strconv.FormatInt(now.UnixMilli(), 10),
		r2BackupLastErrorName:     truncateR2StatusError(runErr.Error()),
	}
	if scheduledDate != "" {
		values[r2BackupLastScheduledAttemptName] = strconv.FormatInt(now.UnixMilli(), 10)
	}
	return writeR2ConfigValues(values)
}

func truncateR2StatusError(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > r2StatusErrorMaxLength {
		runes = runes[:r2StatusErrorMaxLength]
	}
	return string(runes)
}

func recordR2BackupSuccess(summary *R2BackupRunResult, scheduledDate, lastError string) error {
	values := map[string]string{
		r2BackupLastAttemptAtName: strconv.FormatInt(summary.CompletedAt, 10),
		r2BackupLastSuccessAtName: strconv.FormatInt(summary.CompletedAt, 10),
		r2BackupLastObjectKeyName: summary.ObjectKey,
		r2BackupLastErrorName:     truncateR2StatusError(lastError),
		r2BackupLastSizeName:      strconv.FormatInt(summary.Size, 10),
		r2BackupLastSHA256Name:    summary.SHA256,
	}
	if scheduledDate != "" {
		values[r2BackupLastScheduledDateName] = scheduledDate
	}
	return writeR2ConfigValues(values)
}
