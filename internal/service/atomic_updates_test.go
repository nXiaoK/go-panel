package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

type atomicUpdatesFixture struct {
	owner    model.User
	other    model.User
	forwards []model.Forward // [0], [1] owned by owner; [2] owned by other
}

func setupAtomicUpdatesFixture(t *testing.T) atomicUpdatesFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	hashed, err := crypto.HashPassword("old-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	owner := model.User{
		User: "owner-user", Pwd: hashed, RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: 1, Num: 5, CreatedTime: now, Status: model.UserStatusActive,
	}
	other := model.User{
		User: "other-user", Pwd: hashed, RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: 1, Num: 5, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatalf("create other: %v", err)
	}
	forwards := []model.Forward{
		{UserID: owner.ID, UserName: owner.User, Name: "f-1", TunnelID: 1, InPort: 10001, RemoteAddr: "192.0.2.10:80", CreatedTime: now, UpdatedTime: now, Status: 1, Inx: 1},
		{UserID: owner.ID, UserName: owner.User, Name: "f-2", TunnelID: 1, InPort: 10002, RemoteAddr: "192.0.2.10:81", CreatedTime: now, UpdatedTime: now, Status: 1, Inx: 2},
		{UserID: other.ID, UserName: other.User, Name: "f-3", TunnelID: 1, InPort: 10003, RemoteAddr: "192.0.2.10:82", CreatedTime: now, UpdatedTime: now, Status: 1, Inx: 3},
	}
	for i := range forwards {
		if err := model.DB.Create(&forwards[i]).Error; err != nil {
			t.Fatalf("create forward %d: %v", i, err)
		}
	}
	return atomicUpdatesFixture{owner: owner, other: other, forwards: forwards}
}

func loadForwardByID(t *testing.T, id int64) model.Forward {
	t.Helper()
	var forward model.Forward
	if err := model.DB.First(&forward, id).Error; err != nil {
		t.Fatalf("load forward %d: %v", id, err)
	}
	return forward
}

func TestUpdateUserUsernamePropagatesToForwards(t *testing.T) {
	fx := setupAtomicUpdatesFixture(t)
	res := UpdateUser(dto.UserUpdateDto{
		ID: fx.owner.ID, User: "renamed-user", Flow: 100, Num: 5,
		ExpTime: time.Now().Add(time.Hour).UnixMilli(), FlowResetTime: 1,
	})
	if res.Code != 0 {
		t.Fatalf("update user failed: %+v", res)
	}
	for _, id := range []int64{fx.forwards[0].ID, fx.forwards[1].ID} {
		if got := loadForwardByID(t, id).UserName; got != "renamed-user" {
			t.Fatalf("forward %d user_name=%q, want renamed-user", id, got)
		}
	}
	if got := loadForwardByID(t, fx.forwards[2].ID).UserName; got != fx.other.User {
		t.Fatalf("other user's forward renamed to %q", got)
	}
}

func TestUpdatePasswordUsernamePropagatesToForwards(t *testing.T) {
	fx := setupAtomicUpdatesFixture(t)
	res := UpdatePassword(fx.owner.ID, dto.ChangePasswordDto{
		NewUsername:     "self-renamed",
		CurrentPassword: "old-password",
		NewPassword:     "new-password-1",
		ConfirmPassword: "new-password-1",
	})
	if res.Code != 0 {
		t.Fatalf("update password failed: %+v", res)
	}
	for _, id := range []int64{fx.forwards[0].ID, fx.forwards[1].ID} {
		if got := loadForwardByID(t, id).UserName; got != "self-renamed" {
			t.Fatalf("forward %d user_name=%q, want self-renamed", id, got)
		}
	}
}

func TestUpdateForwardOrderIsAtomicAcrossInvalidRows(t *testing.T) {
	fx := setupAtomicUpdatesFixture(t)
	cu := CurrentUser{UserID: fx.owner.ID, RoleID: 1, UserName: fx.owner.User}
	res := UpdateForwardOrder(cu, []map[string]interface{}{
		{"id": fx.forwards[0].ID, "inx": 10},
		{"id": fx.forwards[2].ID, "inx": 11}, // owned by another user
	})
	if res.Code == 0 {
		t.Fatalf("order update with foreign row should fail: %+v", res)
	}
	if got := loadForwardByID(t, fx.forwards[0].ID).Inx; got != 1 {
		t.Fatalf("first row inx=%d, want unchanged 1", got)
	}
	if got := loadForwardByID(t, fx.forwards[2].ID).Inx; got != 3 {
		t.Fatalf("foreign row inx=%d, want unchanged 3", got)
	}
}

func TestUpdateForwardOrderRejectsDuplicateIDs(t *testing.T) {
	fx := setupAtomicUpdatesFixture(t)
	cu := CurrentUser{UserID: fx.owner.ID, RoleID: 1, UserName: fx.owner.User}
	res := UpdateForwardOrder(cu, []map[string]interface{}{
		{"id": fx.forwards[0].ID, "inx": 10},
		{"id": fx.forwards[0].ID, "inx": 11},
	})
	if res.Code == 0 {
		t.Fatalf("duplicate ids should fail: %+v", res)
	}
	if got := loadForwardByID(t, fx.forwards[0].ID).Inx; got != 1 {
		t.Fatalf("inx=%d, want unchanged 1", got)
	}
}

func TestUpdateForwardOrderAppliesAllRows(t *testing.T) {
	fx := setupAtomicUpdatesFixture(t)
	cu := CurrentUser{UserID: fx.owner.ID, RoleID: 1, UserName: fx.owner.User}
	res := UpdateForwardOrder(cu, []map[string]interface{}{
		{"id": fx.forwards[0].ID, "inx": 20},
		{"id": fx.forwards[1].ID, "inx": 21},
	})
	if res.Code != 0 {
		t.Fatalf("order update failed: %+v", res)
	}
	if got := loadForwardByID(t, fx.forwards[0].ID).Inx; got != 20 {
		t.Fatalf("inx=%d, want 20", got)
	}
	if got := loadForwardByID(t, fx.forwards[1].ID).Inx; got != 21 {
		t.Fatalf("inx=%d, want 21", got)
	}
}
