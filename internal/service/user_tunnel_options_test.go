package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
)

func userTunnelItems(t *testing.T, userID int64, roleID int) []dto.TunnelListItem {
	t.Helper()
	res := UserTunnelList(userID, roleID)
	if res.Code != 0 {
		t.Fatalf("list failed: %+v", res)
	}
	items, ok := res.Data.([]dto.TunnelListItem)
	if !ok {
		t.Fatalf("unexpected data: %T", res.Data)
	}
	return items
}

func TestUserTunnelListMarksUnavailableGrants(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&fx.user).Update("num", 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(fx.userTunnel).Update("num", 100).Error; err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{fx.tunnel.ID: ""}
	expired := time.Now().Add(-time.Hour).UnixMilli()
	future := time.Now().Add(time.Hour).UnixMilli()
	for _, tc := range []struct {
		name, reason string
		status       int
		exp          *int64
		used         int64
	}{
		{"disabled", "隧道被禁用", 0, nil, 0},
		{"expired", "该隧道权限已到期", 1, &expired, 0},
		{"exhausted", "该隧道流量已用完", 1, nil, 1 << 30},
	} {
		tunnel := fx.tunnel
		tunnel.ID = 0
		tunnel.Name = tc.name
		if err := model.DB.Create(&tunnel).Error; err != nil {
			t.Fatal(err)
		}
		if tc.exp == nil {
			tc.exp = &future
		}
		grant := model.UserTunnel{UserID: fx.user.ID, TunnelID: tunnel.ID, Num: 100, Flow: 1, InFlow: tc.used, ExpTime: tc.exp, Status: 1}
		if err := model.DB.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&grant).Update("status", tc.status).Error; err != nil {
			t.Fatal(err)
		}
		want[tunnel.ID] = tc.reason
	}
	items := userTunnelItems(t, fx.user.ID, 1)
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	for _, item := range items {
		reason := want[item.ID]
		if item.Available != (reason == "") || item.UnavailableReason != reason {
			t.Errorf("item=%+v want reason %q", item, reason)
		}
		perm := checkUserPermissionsDB(model.DB, CurrentUser{UserID: fx.user.ID, RoleID: 1}, &model.Tunnel{ID: item.ID}, nil)
		if perm.errMsg != reason {
			t.Errorf("permission=%q list=%q", perm.errMsg, item.UnavailableReason)
		}
	}
}

func TestUserTunnelListIncludesAccountAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		changes      map[string]interface{}
	}{
		{"disabled", "用户已到期或被禁用", map[string]interface{}{"status": model.UserStatusDisabled}},
		{"expired", "当前账号已到期", map[string]interface{}{"exp_time": time.Now().Add(-time.Hour).UnixMilli()}},
		{"flow", "用户总流量已用完", map[string]interface{}{"in_flow": int64(100) << 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupFlowAuthTestDB(t, true)
			if err := model.DB.Model(&fx.user).Updates(tc.changes).Error; err != nil {
				t.Fatal(err)
			}
			item := userTunnelItems(t, fx.user.ID, 1)[0]
			perm := checkUserPermissionsDB(model.DB, CurrentUser{UserID: fx.user.ID, RoleID: 1}, &fx.tunnel, nil)
			if item.Available || item.UnavailableReason != tc.reason || perm.errMsg != tc.reason {
				t.Fatalf("item=%+v permission=%q", item, perm.errMsg)
			}
			adminItem := userTunnelItems(t, fx.user.ID, adminRoleID)[0]
			if !adminItem.Available || adminItem.UnavailableReason != "" {
				t.Fatalf("admin item=%+v", adminItem)
			}
		})
	}
}

func TestUserTunnelListDoesNotLeakNodeDetails(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&fx.tunnel).Updates(map[string]interface{}{"relay_node_id": fx.nodeB.ID, "type": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&fx.nodeA).Update("forward_mode", "nftables").Error; err != nil {
		t.Fatal(err)
	}
	items := userTunnelItems(t, fx.user.ID, 1)
	if !items[0].HasRelay || items[0].InNodeForwardMode != "nftables" || *items[0].InNodePortSta != fx.nodeA.PortSta {
		t.Fatalf("missing form metadata: %+v", items[0])
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"outNodeId", "inNodeId", "relayNodeId", "serverIp", "secret", "outIp", fx.nodeA.Name, fx.nodeB.IP} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("leaked %q: %s", forbidden, raw)
		}
	}
}

func TestUserTunnelListBatchesEntryNodeQueries(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	for i := 0; i < 5; i++ {
		tunnel := fx.tunnel
		tunnel.ID = 0
		if err := model.DB.Create(&tunnel).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Create(&model.UserTunnel{UserID: fx.user.ID, TunnelID: tunnel.ID, Status: 1, ExpTime: fx.userTunnel.ExpTime}).Error; err != nil {
			t.Fatal(err)
		}
	}
	queries := 0
	callback := "test_user_tunnel_entry_queries"
	if err := model.DB.Callback().Query().Before("gorm:query").Register(callback, func(db *gorm.DB) {
		if db.Statement.Table == "node" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer model.DB.Callback().Query().Remove(callback)
	if len(userTunnelItems(t, fx.user.ID, 1)) != 6 {
		t.Fatal("missing tunnels")
	}
	if queries != 1 {
		t.Fatalf("entry queries=%d, want 1", queries)
	}
}
