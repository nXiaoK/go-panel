package service

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAttemptLimited     = errors.New("credential attempts limited")
	ErrAccountDisabled    = errors.New("account disabled")
	ErrAccountExpired     = errors.New("account expired")
	ErrCredentialStore    = errors.New("credential store unavailable")
)

var credentialAttemptLimiter = NewAttemptLimiter(time.Now)

func AuthenticateCredentials(username, password, clientIP string) (*model.User, error) {
	limiter := credentialAttemptLimiter
	if !limiter.Allow(clientIP, username) {
		return nil, ErrAttemptLimited
	}
	reserved := true
	defer func() {
		if reserved {
			limiter.Cancel(clientIP, username)
		}
	}()
	commitFailure := func() {
		limiter.Failure(clientIP, username)
		reserved = false
	}
	commitSuccess := func() {
		limiter.Success(clientIP, username)
		reserved = false
	}

	var user model.User
	if err := model.DB.Session(&gorm.Session{Logger: logger.Discard}).Where("user = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			commitFailure()
			return nil, ErrInvalidCredentials
		}
		return nil, credentialStoreError(err)
	}

	now := time.Now()
	nowMillis := now.UnixMilli()
	if user.LoginLockedUntil != nil {
		if *user.LoginLockedUntil > nowMillis {
			return nil, ErrAttemptLimited
		}
		if err := resetExpiredCredentialLock(user.ID, *user.LoginLockedUntil, nowMillis); err != nil {
			if errors.Is(err, ErrAttemptLimited) {
				return nil, ErrAttemptLimited
			}
			return nil, credentialStoreError(err)
		}
		user.LoginFailCount = 0
		user.LoginLockedUntil = nil
	}

	if !crypto.VerifyPassword(user.Pwd, password) {
		commitFailure()
		if err := recordCredentialFailure(user.ID, now); err != nil {
			return nil, credentialStoreError(err)
		}
		return nil, ErrInvalidCredentials
	}

	if err := clearCredentialFailures(user.ID); err != nil {
		return nil, credentialStoreError(err)
	}
	commitSuccess()
	user.LoginFailCount = 0
	user.LoginLockedUntil = nil

	if !isActiveUserStatus(user.Status) {
		return nil, ErrAccountDisabled
	}
	if user.ExpTime != nil && *user.ExpTime <= nowMillis {
		return nil, ErrAccountExpired
	}
	return &user, nil
}

func credentialStoreError(err error) error {
	if errors.Is(err, ErrCredentialStore) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrCredentialStore, err)
}

func resetExpiredCredentialLock(userID, observedLockedUntil, nowMillis int64) error {
	const maxStateTransitions = 3
	lockedUntil := observedLockedUntil
	for attempt := 0; attempt < maxStateTransitions; attempt++ {
		if lockedUntil > nowMillis {
			return ErrAttemptLimited
		}
		result := model.DB.Model(&model.User{}).
			Where("id = ? AND login_locked_until = ? AND login_locked_until <= ?", userID, lockedUntil, nowMillis).
			Updates(map[string]interface{}{
				"login_fail_count":   0,
				"login_locked_until": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}

		var state struct {
			LoginLockedUntil *int64 `gorm:"column:login_locked_until"`
		}
		if err := model.DB.Model(&model.User{}).Select("login_locked_until").Where("id = ?", userID).First(&state).Error; err != nil {
			return err
		}
		if state.LoginLockedUntil == nil {
			return nil
		}
		lockedUntil = *state.LoginLockedUntil
	}
	return errors.New("credential lock changed during reset")
}

func recordCredentialFailure(userID int64, now time.Time) error {
	lockedUntil := now.Add(loginLockDuration).UnixMilli()
	return model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"login_fail_count": gorm.Expr("login_fail_count + 1"),
		"login_locked_until": gorm.Expr(
			"CASE WHEN login_fail_count + 1 >= ? THEN ? ELSE login_locked_until END",
			maxLoginFailCount,
			lockedUntil,
		),
	}).Error
}

func clearCredentialFailures(userID int64) error {
	return model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"login_fail_count":   0,
		"login_locked_until": nil,
	}).Error
}
