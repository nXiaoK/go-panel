package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
)

func useCredentialAttemptLimiter(t *testing.T, limiter *AttemptLimiter) {
	t.Helper()
	previous := credentialAttemptLimiter
	credentialAttemptLimiter = limiter
	t.Cleanup(func() {
		credentialAttemptLimiter = previous
	})
}

func failCredentialUpdates(t *testing.T, injected error) {
	t.Helper()
	name := "test:fail-credential-updates:" + t.Name()
	callbacks := model.DB.Callback().Update()
	if err := callbacks.Before("gorm:update").Register(name, func(db *gorm.DB) {
		db.AddError(injected)
	}); err != nil {
		t.Fatalf("register update failure callback: %v", err)
	}
	t.Cleanup(func() { _ = callbacks.Remove(name) })
}

func TestAuthenticateCredentialsExpiredLoginLockResetsBeforeFailure(t *testing.T) {
	initUserTestDB(t)

	expiredLock := time.Now().Add(-time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"login_fail_count":   maxLoginFailCount,
		"login_locked_until": expiredLock,
	}).Error; err != nil {
		t.Fatalf("prepare expired login lock: %v", err)
	}

	_, err := AuthenticateCredentials("admin_user", "wrong password", "192.0.2.101")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error=%v, want invalid credentials", err)
	}

	var got model.User
	if err := model.DB.First(&got, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.LoginFailCount != 1 {
		t.Fatalf("failure count=%d, want 1 after expired lock reset", got.LoginFailCount)
	}
	if got.LoginLockedUntil != nil {
		t.Fatalf("lock=%d, want cleared lock", *got.LoginLockedUntil)
	}
}

func TestAuthenticateCredentialsFailureCountUsesAtomicIncrement(t *testing.T) {
	initUserTestDB(t)

	const failures = maxLoginFailCount
	start := make(chan struct{})
	errs := make(chan error, failures)
	var wg sync.WaitGroup
	for i := 0; i < failures; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := AuthenticateCredentials("admin_user", "wrong password", fmt.Sprintf("192.0.2.%d", 110+i))
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrInvalidCredentials) && !errors.Is(err, ErrAttemptLimited) {
			t.Fatalf("concurrent authentication error=%v, want invalid credentials or an established lock", err)
		}
	}

	var got model.User
	if err := model.DB.First(&got, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.LoginFailCount != failures {
		t.Fatalf("failure count=%d, want %d", got.LoginFailCount, failures)
	}
	if got.LoginLockedUntil == nil || *got.LoginLockedUntil <= time.Now().UnixMilli() {
		t.Fatal("fifth persistent failure must create a future lock")
	}
}

func TestAuthenticateCredentialsRejectsActivePersistentLock(t *testing.T) {
	initUserTestDB(t)

	activeLock := time.Now().Add(time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"login_fail_count":   maxLoginFailCount,
		"login_locked_until": activeLock,
	}).Error; err != nil {
		t.Fatalf("prepare active login lock: %v", err)
	}

	_, err := AuthenticateCredentials("admin_user", userTestAdminPassword, "192.0.2.121")
	if !errors.Is(err, ErrAttemptLimited) {
		t.Fatalf("error=%v, want attempt limited", err)
	}
}

func TestAuthenticateCredentialsLimitsUnknownUserAttempts(t *testing.T) {
	initUserTestDB(t)

	for i := 0; i < pairAttemptLimit; i++ {
		_, err := AuthenticateCredentials("missing-user", "wrong password", "192.0.2.122")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error=%v, want invalid credentials", i+1, err)
		}
	}
	_, err := AuthenticateCredentials("missing-user", "wrong password", "192.0.2.122")
	if !errors.Is(err, ErrAttemptLimited) {
		t.Fatalf("sixth error=%v, want attempt limited", err)
	}
}

func TestAuthenticateCredentialsChecksAccountStateAfterSuccessfulPassword(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		expiresAt int64
		wantError error
		clientIP  string
	}{
		{
			name:      "disabled",
			status:    userStatusDisabled,
			expiresAt: time.Now().Add(time.Hour).UnixMilli(),
			wantError: ErrAccountDisabled,
			clientIP:  "192.0.2.123",
		},
		{
			name:      "expired",
			status:    userStatusActive,
			expiresAt: time.Now().Add(-time.Minute).UnixMilli(),
			wantError: ErrAccountExpired,
			clientIP:  "192.0.2.124",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initUserTestDB(t)
			if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
				"status":           tt.status,
				"exp_time":         tt.expiresAt,
				"login_fail_count": 2,
			}).Error; err != nil {
				t.Fatalf("prepare account: %v", err)
			}

			_, err := AuthenticateCredentials("admin_user", userTestAdminPassword, tt.clientIP)
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("error=%v, want %v", err, tt.wantError)
			}

			var got model.User
			if err := model.DB.First(&got, 1).Error; err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if got.LoginFailCount != 0 || got.LoginLockedUntil != nil {
				t.Fatalf("successful password did not clear persistent failures: count=%d lock=%v", got.LoginFailCount, got.LoginLockedUntil)
			}
		})
	}
}

func TestAuthenticateCredentialsReturnsActiveUser(t *testing.T) {
	initUserTestDB(t)

	user, err := AuthenticateCredentials("admin_user", userTestAdminPassword, "192.0.2.125")
	if err != nil {
		t.Fatalf("authenticate active user: %v", err)
	}
	if user.ID != 1 || user.User != "admin_user" {
		t.Fatalf("unexpected user: %#v", user)
	}
}

func TestExpiredLockResetDoesNotClearConcurrentNewLock(t *testing.T) {
	initUserTestDB(t)

	now := time.Now().UnixMilli()
	expiredLock := now - int64(time.Minute/time.Millisecond)
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"login_fail_count":   maxLoginFailCount,
		"login_locked_until": expiredLock,
	}).Error; err != nil {
		t.Fatalf("prepare expired lock: %v", err)
	}

	var stale model.User
	if err := model.DB.Select("id, login_locked_until").First(&stale, 1).Error; err != nil {
		t.Fatalf("read stale lock: %v", err)
	}
	concurrentLock := now + int64(time.Minute/time.Millisecond)
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"login_fail_count":   maxLoginFailCount,
		"login_locked_until": concurrentLock,
	}).Error; err != nil {
		t.Fatalf("establish concurrent lock: %v", err)
	}

	err := resetExpiredCredentialLock(stale.ID, *stale.LoginLockedUntil, now)
	if !errors.Is(err, ErrAttemptLimited) {
		t.Fatalf("reset error=%v, want concurrent lock to limit the stale request", err)
	}

	var got model.User
	if err := model.DB.First(&got, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.LoginLockedUntil == nil || *got.LoginLockedUntil != concurrentLock {
		t.Fatalf("concurrent lock was cleared: got=%v want=%d", got.LoginLockedUntil, concurrentLock)
	}
	if got.LoginFailCount != maxLoginFailCount {
		t.Fatalf("failure count=%d, want concurrent state preserved", got.LoginFailCount)
	}
}

func TestAuthenticateCredentialsPropagatesRecordFailureError(t *testing.T) {
	initUserTestDB(t)
	limiter := NewAttemptLimiter(time.Now)
	useCredentialAttemptLimiter(t, limiter)
	for i := 0; i < pairAttemptLimit-1; i++ {
		recordLimiterFailure(t, limiter, "192.0.2.130", "admin_user")
	}
	injected := errors.New("injected record failure")
	failCredentialUpdates(t, injected)

	_, err := AuthenticateCredentials("admin_user", "wrong password", "192.0.2.130")
	if !errors.Is(err, ErrCredentialStore) || !errors.Is(err, injected) {
		t.Fatalf("error=%v, want wrapped credential store failure", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("record failure was disguised as invalid credentials: %v", err)
	}
	if limiter.Allow("192.0.2.130", "admin_user") {
		t.Fatal("wrong password was not committed to the in-memory limiter after record failure")
	}
}

func TestAuthenticateCredentialsPropagatesClearFailureAndCancelsReservation(t *testing.T) {
	initUserTestDB(t)
	limiter := NewAttemptLimiter(time.Now)
	useCredentialAttemptLimiter(t, limiter)
	for i := 0; i < pairAttemptLimit-1; i++ {
		recordLimiterFailure(t, limiter, "192.0.2.131", "admin_user")
	}
	injected := errors.New("injected clear failure")
	failCredentialUpdates(t, injected)

	_, err := AuthenticateCredentials("admin_user", userTestAdminPassword, "192.0.2.131")
	if !errors.Is(err, ErrCredentialStore) || !errors.Is(err, injected) {
		t.Fatalf("error=%v, want wrapped credential store failure", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("clear failure was disguised as invalid credentials: %v", err)
	}
	if !limiter.Allow("192.0.2.131", "admin_user") {
		t.Fatal("clear failure leaked the fifth in-flight reservation")
	}
	limiter.Cancel("192.0.2.131", "admin_user")
}

func TestAuthenticateCredentialsClosedDatabaseFailsClosedAndCancelsReservation(t *testing.T) {
	initUserTestDB(t)
	limiter := NewAttemptLimiter(time.Now)
	useCredentialAttemptLimiter(t, limiter)
	for i := 0; i < pairAttemptLimit-1; i++ {
		recordLimiterFailure(t, limiter, "192.0.2.132", "admin_user")
	}
	if err := model.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	user, err := AuthenticateCredentials("admin_user", userTestAdminPassword, "192.0.2.132")
	if user != nil {
		t.Fatalf("closed database authentication returned user: %#v", user)
	}
	if !errors.Is(err, ErrCredentialStore) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error=%v, want non-credential store failure", err)
	}
	if !limiter.Allow("192.0.2.132", "admin_user") {
		t.Fatal("database read failure leaked or committed the in-flight reservation")
	}
	limiter.Cancel("192.0.2.132", "admin_user")
}

func TestAuthenticateCredentialsActiveLockCancelsReservation(t *testing.T) {
	initUserTestDB(t)
	limiter := NewAttemptLimiter(time.Now)
	useCredentialAttemptLimiter(t, limiter)
	for i := 0; i < pairAttemptLimit-1; i++ {
		recordLimiterFailure(t, limiter, "192.0.2.133", "admin_user")
	}
	activeLock := time.Now().Add(time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]interface{}{
		"login_fail_count":   maxLoginFailCount,
		"login_locked_until": activeLock,
	}).Error; err != nil {
		t.Fatalf("prepare active lock: %v", err)
	}

	_, err := AuthenticateCredentials("admin_user", userTestAdminPassword, "192.0.2.133")
	if !errors.Is(err, ErrAttemptLimited) {
		t.Fatalf("error=%v, want attempt limited", err)
	}
	if !limiter.Allow("192.0.2.133", "admin_user") {
		t.Fatal("active database lock leaked the in-flight reservation")
	}
	limiter.Cancel("192.0.2.133", "admin_user")
}

func TestLoginDoesNotDisguiseCredentialStoreFailure(t *testing.T) {
	initUserTestDB(t)
	limiter := NewAttemptLimiter(time.Now)
	useCredentialAttemptLimiter(t, limiter)
	failCredentialUpdates(t, errors.New("injected login clear failure"))

	res := Login(dto.LoginDto{Username: "admin_user", Password: userTestAdminPassword}, "192.0.2.134")
	if res.Code == 0 {
		t.Fatalf("login succeeded while credential state could not be cleared: %#v", res)
	}
	if res.Msg != "认证服务暂不可用，请稍后重试" {
		t.Fatalf("msg=%q, want credential store unavailable", res.Msg)
	}
}
