package service

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

const userTestAdminPassword = "test admin password"

func initUserTestDB(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db"), model.BootstrapOptions{
		AdminPassword: userTestAdminPassword,
	}); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = model.Close()
	})
	// Isolate the process-global credential limiter so login tests do not
	// share pair/IP failure windows across cases (or -count re-runs).
	useCredentialAttemptLimiter(t, NewAttemptLimiter(time.Now))
	crypto.InitJwt("test-secret")
}

func TestLoginRejectsNonActiveStatus(t *testing.T) {
	initUserTestDB(t)

	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("status", 2).Error; err != nil {
		t.Fatalf("set status: %v", err)
	}

	res := Login(dto.LoginDto{Username: "admin_user", Password: userTestAdminPassword}, "192.0.2.201")
	if res.Code == 0 {
		t.Fatalf("Login succeeded for non-active status: %#v", res)
	}
	if res.Msg != "账号已被禁用" {
		t.Fatalf("msg=%q, want account disabled", res.Msg)
	}
}

func TestCreateUserRejectsInvalidStatus(t *testing.T) {
	initUserTestDB(t)

	status := 2
	res := CreateUser(dto.UserDto{
		User:          "bad-status",
		Pwd:           "secret-pass",
		Flow:          1,
		Num:           1,
		ExpTime:       time.Now().Add(time.Hour).UnixMilli(),
		FlowResetTime: 1,
		Status:        &status,
	})
	if res.Code == 0 {
		t.Fatalf("CreateUser succeeded for invalid status: %#v", res)
	}
	if res.Msg != "用户状态参数错误" {
		t.Fatalf("msg=%q, want invalid status error", res.Msg)
	}

	var count int64
	model.DB.Model(&model.User{}).Where("user = ?", "bad-status").Count(&count)
	if count != 0 {
		t.Fatalf("created %d users with invalid status", count)
	}
}

func TestUpdateUserRejectsInvalidStatus(t *testing.T) {
	initUserTestDB(t)

	exp := time.Now().Add(time.Hour).UnixMilli()
	pwd, err := crypto.HashPassword("secret-pass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := model.User{
		User:          "normal-user",
		Pwd:           pwd,
		RoleID:        userRoleID,
		ExpTime:       &exp,
		Flow:          1,
		FlowResetTime: 1,
		Num:           1,
		CreatedTime:   time.Now().UnixMilli(),
		Status:        userStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	status := 2
	res := UpdateUser(dto.UserUpdateDto{
		ID:            user.ID,
		User:          user.User,
		Flow:          user.Flow,
		Num:           user.Num,
		ExpTime:       exp,
		FlowResetTime: user.FlowResetTime,
		Status:        &status,
	})
	if res.Code == 0 {
		t.Fatalf("UpdateUser succeeded for invalid status: %#v", res)
	}
	if res.Msg != "用户状态参数错误" {
		t.Fatalf("msg=%q, want invalid status error", res.Msg)
	}

	var got model.User
	if err := model.DB.First(&got, user.ID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if got.Status != userStatusActive {
		t.Fatalf("status=%d, want unchanged active status", got.Status)
	}
}

func TestLoginTokenCarriesCurrentTokenVersion(t *testing.T) {
	initUserTestDB(t)

	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("token_version", 3).Error; err != nil {
		t.Fatalf("set token version: %v", err)
	}
	res := Login(dto.LoginDto{Username: "admin_user", Password: userTestAdminPassword}, "192.0.2.202")
	if res.Code != 0 {
		t.Fatalf("login failed: %#v", res)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("login data type=%T", res.Data)
	}
	token, ok := data["token"].(string)
	if !ok || token == "" {
		t.Fatalf("missing login token: %#v", data)
	}
	claims, err := crypto.ParseToken(token)
	if err != nil {
		t.Fatalf("parse login token: %v", err)
	}
	if claims.TokenVersion != 3 {
		t.Fatalf("token version=%d, want 3", claims.TokenVersion)
	}
}

func TestLoginRejectsExpiredAccount(t *testing.T) {
	initUserTestDB(t)

	expiresAt := time.Now().Add(-time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("exp_time", expiresAt).Error; err != nil {
		t.Fatalf("expire account: %v", err)
	}

	res := Login(dto.LoginDto{Username: "admin_user", Password: userTestAdminPassword}, "192.0.2.203")
	if res.Code == 0 {
		t.Fatalf("Login succeeded for expired account: %#v", res)
	}
	if res.Msg != "账号已过期" {
		t.Fatalf("msg=%q, want account expired", res.Msg)
	}
}

func TestLoginMapsSixthAttemptToLimitedResult(t *testing.T) {
	initUserTestDB(t)

	request := dto.LoginDto{Username: "admin_user", Password: "wrong password"}
	for i := 0; i < pairAttemptLimit; i++ {
		res := Login(request, "192.0.2.204")
		if res.Code == 0 || res.Msg != "账号或密码错误" {
			t.Fatalf("attempt %d result=%#v, want invalid credentials", i+1, res)
		}
	}

	res := Login(dto.LoginDto{Username: "admin_user", Password: userTestAdminPassword}, "192.0.2.204")
	if res.Code == 0 {
		t.Fatalf("sixth attempt succeeded: %#v", res)
	}
	if res.Msg != "登录尝试过多，请稍后重试" {
		t.Fatalf("msg=%q, want attempt limited", res.Msg)
	}
}

func TestGetAllUsersUserListOmitsPassword(t *testing.T) {
	initUserTestDB(t)

	passwordHash, err := crypto.HashPassword("user list sensitive password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{
		User:          "ordinary-user",
		Pwd:           passwordHash,
		TokenVersion:  73,
		RoleID:        userRoleID,
		ExpTime:       &expiresAt,
		FlowResetTime: 1,
		CreatedTime:   time.Now().UnixMilli(),
		Status:        userStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	res := GetAllUsers()
	if res.Code != 0 {
		t.Fatalf("get users failed: %#v", res)
	}
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("marshal user list: %v", err)
	}
	if bytes.Contains(raw, []byte(passwordHash)) || bytes.Contains(raw, []byte(`"pwd"`)) {
		t.Fatalf("password leaked into user list: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"tokenVersion"`)) || bytes.Contains(raw, []byte(`"ver"`)) {
		t.Fatalf("token version leaked into user list: %s", raw)
	}
}

func TestUpdatePasswordIncrementsTokenVersion(t *testing.T) {
	initUserTestDB(t)

	var before model.User
	if err := model.DB.First(&before, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	oldToken, err := crypto.GenerateToken(before.ID, before.User, before.RoleID, before.TokenVersion)
	if err != nil {
		t.Fatalf("generate old token: %v", err)
	}
	res := UpdatePassword(before.ID, dto.ChangePasswordDto{
		NewUsername:     "renamed-admin",
		CurrentPassword: userTestAdminPassword,
		NewPassword:     "replacement admin password",
		ConfirmPassword: "replacement admin password",
	})
	if res.Code != 0 {
		t.Fatalf("update password failed: %#v", res)
	}

	var got model.User
	if err := model.DB.First(&got, before.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.TokenVersion != before.TokenVersion+1 {
		t.Fatalf("token version=%d, want %d", got.TokenVersion, before.TokenVersion+1)
	}
	if got.User != "renamed-admin" {
		t.Fatalf("username=%q, want renamed-admin", got.User)
	}
	if !crypto.VerifyPassword(got.Pwd, "replacement admin password") {
		t.Fatal("replacement password was not stored")
	}
	oldClaims, err := crypto.ParseToken(oldToken)
	if err != nil {
		t.Fatalf("parse old token: %v", err)
	}
	if oldClaims.TokenVersion == got.TokenVersion {
		t.Fatalf("old token version=%d was not revoked", oldClaims.TokenVersion)
	}
}

func TestUpdateUserPasswordResetIncrementsTokenVersion(t *testing.T) {
	initUserTestDB(t)

	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	passwordHash, err := crypto.HashPassword("ordinary old password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := model.User{
		User:          "ordinary-user",
		Pwd:           passwordHash,
		TokenVersion:  9,
		RoleID:        userRoleID,
		ExpTime:       &expiresAt,
		Flow:          1,
		FlowResetTime: 1,
		Num:           1,
		CreatedTime:   time.Now().UnixMilli(),
		Status:        userStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	oldToken, err := crypto.GenerateToken(user.ID, user.User, user.RoleID, user.TokenVersion)
	if err != nil {
		t.Fatalf("generate old token: %v", err)
	}

	res := UpdateUser(dto.UserUpdateDto{
		ID:            user.ID,
		User:          "renamed-user",
		Pwd:           "ordinary replacement password",
		Flow:          user.Flow,
		Num:           user.Num,
		ExpTime:       expiresAt,
		FlowResetTime: user.FlowResetTime,
	})
	if res.Code != 0 {
		t.Fatalf("reset password failed: %#v", res)
	}

	var got model.User
	if err := model.DB.First(&got, user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.TokenVersion != user.TokenVersion+1 {
		t.Fatalf("token version=%d, want %d", got.TokenVersion, user.TokenVersion+1)
	}
	if got.User != "renamed-user" {
		t.Fatalf("username=%q, want renamed-user", got.User)
	}
	if !crypto.VerifyPassword(got.Pwd, "ordinary replacement password") {
		t.Fatal("replacement password was not stored")
	}
	oldClaims, err := crypto.ParseToken(oldToken)
	if err != nil {
		t.Fatalf("parse old token: %v", err)
	}
	if oldClaims.TokenVersion == got.TokenVersion {
		t.Fatalf("old token version=%d was not revoked", oldClaims.TokenVersion)
	}
}
