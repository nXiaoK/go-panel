package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

type flowAuthFixture struct {
	nodeA      model.Node
	nodeB      model.Node
	user       model.User
	tunnel     model.Tunnel
	forward    model.Forward
	userTunnel *model.UserTunnel
}

func setupFlowAuthTestDB(t *testing.T, withUserTunnel bool) flowAuthFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	nodeA := model.Node{
		Name: "entry-a", Secret: "entry-a-secret", IP: "192.0.2.1", ServerIP: "192.0.2.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeGost, CreatedTime: now, Status: 1,
	}
	nodeB := model.Node{
		Name: "entry-b", Secret: "entry-b-secret", IP: "192.0.2.2", ServerIP: "192.0.2.2",
		PortSta: 20001, PortEnd: 30000, ForwardMode: forwardModeGost, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&nodeA).Error; err != nil {
		t.Fatalf("create node A: %v", err)
	}
	if err := model.DB.Create(&nodeB).Error; err != nil {
		t.Fatalf("create node B: %v", err)
	}
	expires := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{
		User: "flow-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: now, Num: 1, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tunnel := model.Tunnel{
		Name: "flow-tunnel", TrafficRatio: 1, InNodeID: nodeA.ID, InIP: nodeA.IP,
		OutNodeID: nodeA.ID, OutIP: nodeA.IP, Type: tunnelTypePortForward, Flow: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	forward := model.Forward{
		UserID: user.ID, UserName: user.User, Name: "flow-forward", TunnelID: tunnel.ID,
		InPort: 10001, RemoteAddr: "198.51.100.1:443", Strategy: "fifo",
		CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	fixture := flowAuthFixture{nodeA: nodeA, nodeB: nodeB, user: user, tunnel: tunnel, forward: forward}
	if withUserTunnel {
		ut := model.UserTunnel{
			UserID: user.ID, TunnelID: tunnel.ID, Num: 1, Flow: 100,
			FlowResetTime: now, ExpTime: &expires, Status: 1,
		}
		if err := model.DB.Create(&ut).Error; err != nil {
			t.Fatalf("create user tunnel: %v", err)
		}
		fixture.userTunnel = &ut
	}
	return fixture
}

func TestAuthenticateNodeSecretReturnsAndCachesFullContext(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	secret := fixture.nodeA.Secret
	InvalidateSecretCache(secret)
	t.Cleanup(func() { InvalidateSecretCache(secret) })

	node, err := AuthenticateNodeSecret(secret)
	if err != nil {
		t.Fatalf("AuthenticateNodeSecret: %v", err)
	}
	if node != (AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}) {
		t.Fatalf("node=%+v, want id=%d mode=%q", node, fixture.nodeA.ID, forwardModeGost)
	}

	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).
		Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatalf("change node mode: %v", err)
	}
	cached, err := AuthenticateNodeSecret(secret)
	if err != nil {
		t.Fatalf("AuthenticateNodeSecret cached: %v", err)
	}
	if cached != node {
		t.Fatalf("cached node=%+v, want original full context %+v", cached, node)
	}

	InvalidateSecretCache(secret)
	refreshed, err := AuthenticateNodeSecret(secret)
	if err != nil {
		t.Fatalf("AuthenticateNodeSecret refreshed: %v", err)
	}
	if refreshed != (AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}) {
		t.Fatalf("refreshed node=%+v, want updated mode", refreshed)
	}
}

func TestAuthenticateNodeSecretUsesUniformInvalidError(t *testing.T) {
	setupFlowAuthTestDB(t, false)
	for _, secret := range []string{"", "unknown-node-secret"} {
		_, err := AuthenticateNodeSecret(secret)
		if !errors.Is(err, ErrInvalidNodeSecret) {
			t.Fatalf("secret=%q err=%v, want ErrInvalidNodeSecret", secret, err)
		}
		if err != ErrInvalidNodeSecret {
			t.Fatalf("secret=%q returned distinguishable error %v", secret, err)
		}
	}
}

func TestAuthenticateNodeSecretRetriesWhenTargetedInvalidationFollowsDatabaseRead(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	secret := fixture.nodeA.Secret
	InvalidateSecretCache(secret)
	t.Cleanup(func() { InvalidateSecretCache(secret) })
	queryReturned, releaseQuery := blockNextNodeAuthQuery(t)

	result := make(chan struct {
		node AuthenticatedNode
		err  error
	}, 1)
	go func() {
		node, err := AuthenticateNodeSecret(secret)
		result <- struct {
			node AuthenticatedNode
			err  error
		}{node: node, err: err}
	}()
	waitForNodeAuthQuery(t, queryReturned)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).
		Update("forward_mode", forwardModeNftables).Error; err != nil {
		close(releaseQuery)
		t.Fatalf("change node mode: %v", err)
	}
	InvalidateSecretCache(secret)
	close(releaseQuery)

	got := <-result
	if got.err != nil {
		t.Fatalf("AuthenticateNodeSecret: %v", got.err)
	}
	if got.node != (AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}) {
		t.Fatalf("node=%+v, want context re-read after targeted invalidation", got.node)
	}
}

func TestAuthenticateNodeSecretRetriesWhenFullInvalidationFollowsDatabaseRead(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	oldSecret := fixture.nodeA.Secret
	newSecret := "restored-node-secret"
	InvalidateSecretCache(oldSecret)
	t.Cleanup(func() {
		InvalidateSecretCache(oldSecret)
		InvalidateSecretCache(newSecret)
	})
	queryReturned, releaseQuery := blockNextNodeAuthQuery(t)

	result := make(chan error, 1)
	go func() {
		_, err := AuthenticateNodeSecret(oldSecret)
		result <- err
	}()
	waitForNodeAuthQuery(t, queryReturned)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).Updates(map[string]any{
		"secret": newSecret, "forward_mode": forwardModeNftables,
	}).Error; err != nil {
		close(releaseQuery)
		t.Fatalf("replace node identity: %v", err)
	}
	invalidateAllSecretCache()
	close(releaseQuery)

	if err := <-result; !errors.Is(err, ErrInvalidNodeSecret) {
		t.Fatalf("old secret err=%v, want ErrInvalidNodeSecret after full invalidation", err)
	}
	node, err := AuthenticateNodeSecret(newSecret)
	if err != nil {
		t.Fatalf("authenticate replacement node: %v", err)
	}
	if node != (AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}) {
		t.Fatalf("replacement context=%+v", node)
	}
}

func TestAuthenticateNodeSecretRetriesChangedGenerationAfterNotFound(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	newSecret := "newly-restored-secret"
	InvalidateSecretCache(newSecret)
	t.Cleanup(func() { InvalidateSecretCache(newSecret) })
	queryReturned, releaseQuery := blockNextNodeAuthQuery(t)

	result := make(chan struct {
		node AuthenticatedNode
		err  error
	}, 1)
	go func() {
		node, err := AuthenticateNodeSecret(newSecret)
		result <- struct {
			node AuthenticatedNode
			err  error
		}{node: node, err: err}
	}()
	waitForNodeAuthQuery(t, queryReturned)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).Updates(map[string]any{
		"secret": newSecret, "forward_mode": forwardModeNftables,
	}).Error; err != nil {
		close(releaseQuery)
		t.Fatalf("restore matching node identity: %v", err)
	}
	invalidateAllSecretCache()
	close(releaseQuery)

	got := <-result
	if got.err != nil {
		t.Fatalf("AuthenticateNodeSecret did not retry stale not-found result: %v", got.err)
	}
	if got.node != (AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}) {
		t.Fatalf("restored context=%+v", got.node)
	}
}

func blockNextNodeAuthQuery(t *testing.T) (<-chan struct{}, chan struct{}) {
	t.Helper()
	const callbackName = "test:block-node-auth-after-query"
	queryReturned := make(chan struct{}, 1)
	releaseQuery := make(chan struct{})
	var once sync.Once
	if err := model.DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if gormStatementTable(tx) != "node" {
			return
		}
		once.Do(func() {
			queryReturned <- struct{}{}
			<-releaseQuery
		})
	}); err != nil {
		t.Fatalf("register node query barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = model.DB.Callback().Query().Remove(callbackName)
	})
	return queryReturned, releaseQuery
}

func waitForNodeAuthQuery(t *testing.T, queryReturned <-chan struct{}) {
	t.Helper()
	select {
	case <-queryReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for node authentication query")
	}
}

func TestUpdateNodeInvalidatesAuthenticatedContext(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	// nodeA is referenced by a Forward and therefore cannot change runtime
	// mode. Use the idle nodeB so this test continues to exercise cache
	// invalidation after a successful mode mutation.
	secret := fixture.nodeB.Secret
	InvalidateSecretCache(secret)
	t.Cleanup(func() { InvalidateSecretCache(secret) })
	if _, err := AuthenticateNodeSecret(secret); err != nil {
		t.Fatalf("prime authenticated context: %v", err)
	}

	res := UpdateNode(dto.NodeUpdateDto{
		ID: fixture.nodeB.ID, Name: fixture.nodeB.Name, IP: fixture.nodeB.IP, ServerIP: fixture.nodeB.ServerIP,
		PortSta: fixture.nodeB.PortSta, PortEnd: fixture.nodeB.PortEnd, ForwardMode: forwardModeNftables,
	})
	if res.Code != 0 {
		t.Fatalf("UpdateNode returned code=%d msg=%q", res.Code, res.Msg)
	}

	node, err := AuthenticateNodeSecret(secret)
	if err != nil {
		t.Fatalf("AuthenticateNodeSecret after update: %v", err)
	}
	if node.ForwardMode != forwardModeNftables {
		t.Fatalf("cached mode=%q, want updated mode %q", node.ForwardMode, forwardModeNftables)
	}
}

func TestDeleteNodeInvalidatesContextOnlyAfterSuccessfulMutation(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	secret := fixture.nodeB.Secret
	InvalidateSecretCache(secret)
	t.Cleanup(func() { InvalidateSecretCache(secret) })
	if _, err := AuthenticateNodeSecret(secret); err != nil {
		t.Fatalf("prime authenticated context: %v", err)
	}
	trigger := fmt.Sprintf(`
CREATE TRIGGER fail_node_delete
BEFORE DELETE ON node
WHEN OLD.id = %d
BEGIN
  SELECT RAISE(FAIL, 'injected node delete failure');
END`, fixture.nodeB.ID)
	if err := model.DB.Exec(trigger).Error; err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	DeleteNode(fixture.nodeB.ID)
	if _, ok := secretCacheLoad(secret); !ok {
		t.Fatal("failed node deletion invalidated authenticated context")
	}
	if err := model.DB.Exec("DROP TRIGGER fail_node_delete").Error; err != nil {
		t.Fatalf("drop delete trigger: %v", err)
	}

	DeleteNode(fixture.nodeB.ID)
	if _, ok := secretCacheLoad(secret); ok {
		t.Fatal("successful node deletion left authenticated context cached")
	}
	if _, err := AuthenticateNodeSecret(secret); !errors.Is(err, ErrInvalidNodeSecret) {
		t.Fatalf("deleted node secret err=%v, want ErrInvalidNodeSecret", err)
	}
}

func TestApplyGostFlowRejectsAnotherNodesForward(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	before := loadFlowForward(t, fixture.forward.ID)
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeB.ID, ForwardMode: forwardModeGost}, dto.FlowDto{
			N: fmt.Sprintf("%d_%d_0", fixture.forward.ID, fixture.user.ID), U: 100, D: 200,
		})
	})
	if !errors.Is(err, ErrFlowNodeMismatch) {
		t.Fatalf("err=%v, want ErrFlowNodeMismatch", err)
	}
	after := loadFlowForward(t, fixture.forward.ID)
	if after.InFlow != before.InFlow || after.OutFlow != before.OutFlow {
		t.Fatal("mismatched node changed forward counters")
	}
}

func TestApplyGostFlowRejectsForgedUserAndUserTunnel(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	expires := time.Now().Add(time.Hour).UnixMilli()
	otherUser := model.User{
		User: "other-flow-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: time.Now().UnixMilli(), Num: 1, CreatedTime: time.Now().UnixMilli(), Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&otherUser).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherUT := model.UserTunnel{
		UserID: otherUser.ID, TunnelID: fixture.tunnel.ID, Num: 1, Flow: 100,
		FlowResetTime: time.Now().UnixMilli(), ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&otherUT).Error; err != nil {
		t.Fatalf("create forged user tunnel: %v", err)
	}

	tests := []dto.FlowDto{
		{N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, otherUser.ID, fixture.userTunnel.ID), U: 100, D: 200},
		{N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, otherUT.ID), U: 100, D: 200},
	}
	for _, flow := range tests {
		before := loadFlowForward(t, fixture.forward.ID)
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
		})
		if !errors.Is(err, ErrFlowNodeMismatch) {
			t.Fatalf("flow=%q err=%v, want ErrFlowNodeMismatch", flow.N, err)
		}
		after := loadFlowForward(t, fixture.forward.ID)
		if after.InFlow != before.InFlow || after.OutFlow != before.OutFlow {
			t.Fatalf("forged relationship %q changed counters", flow.N)
		}
	}
}

func TestApplyGostFlowUpdatesAllCountersInCallerTransaction(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	flow := dto.FlowDto{
		N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		U: 100,
		D: 200,
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
	})
	if err != nil {
		t.Fatalf("ApplyGostFlow: %v", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	user := loadFlowUser(t, fixture.user.ID)
	userTunnel := loadFlowUserTunnel(t, fixture.userTunnel.ID)
	for label, counters := range map[string][2]int64{
		"forward":     {forward.InFlow, forward.OutFlow},
		"user":        {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{200, 100} {
			t.Fatalf("%s counters=%v, want [200 100]", label, counters)
		}
	}
}

func TestApplyNftFlowItemRejectsAnotherNodesForward(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	before := loadFlowForward(t, fixture.forward.ID)
	item := nftFlowItem(fixture.forward.ID, fixture.user.ID, 0, 100, 200)
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyNftFlowItem(tx, AuthenticatedNode{ID: fixture.nodeB.ID, ForwardMode: forwardModeNftables}, item)
	})
	if !errors.Is(err, ErrFlowNodeMismatch) {
		t.Fatalf("err=%v, want ErrFlowNodeMismatch", err)
	}
	after := loadFlowForward(t, fixture.forward.ID)
	if after.InFlow != before.InFlow || after.OutFlow != before.OutFlow {
		t.Fatal("mismatched nft node changed forward counters")
	}
}

func TestApplyNftFlowItemRejectsForgedUserTunnel(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	expires := time.Now().Add(time.Hour).UnixMilli()
	otherUser := model.User{
		User: "other-nft-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: time.Now().UnixMilli(), Num: 1, CreatedTime: time.Now().UnixMilli(), Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&otherUser).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherUT := model.UserTunnel{
		UserID: otherUser.ID, TunnelID: fixture.tunnel.ID, Num: 1, Flow: 100,
		FlowResetTime: time.Now().UnixMilli(), ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&otherUT).Error; err != nil {
		t.Fatalf("create other user tunnel: %v", err)
	}
	item := nftFlowItem(fixture.forward.ID, fixture.user.ID, otherUT.ID, 100, 200)
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyNftFlowItem(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, item)
	})
	if !errors.Is(err, ErrFlowNodeMismatch) {
		t.Fatalf("err=%v, want ErrFlowNodeMismatch", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 0 || forward.OutFlow != 0 {
		t.Fatal("forged nft user tunnel changed counters")
	}
}

func TestApplyNftFlowItemUpdatesAllCounters(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	item := nftFlowItem(fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID, 100, 200)
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyNftFlowItem(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, item)
	})
	if err != nil {
		t.Fatalf("ApplyNftFlowItem: %v", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	user := loadFlowUser(t, fixture.user.ID)
	userTunnel := loadFlowUserTunnel(t, fixture.userTunnel.ID)
	for label, counters := range map[string][2]int64{
		"forward":     {forward.InFlow, forward.OutFlow},
		"user":        {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{200, 100} {
			t.Fatalf("%s counters=%v, want [200 100]", label, counters)
		}
	}
}

func TestApplyFlowRejectsMalformedReferencesAndWrongProtocol(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	tests := []struct {
		name string
		fn   func(*gorm.DB) error
		want error
	}{
		{
			name: "malformed gost reference",
			fn: func(tx *gorm.DB) error {
				return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, dto.FlowDto{N: "bad", U: 1, D: 1})
			},
			want: ErrInvalidFlowReport,
		},
		{
			name: "malformed nft pointers",
			fn: func(tx *gorm.DB) error {
				return ApplyNftFlowItem(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, dto.NftFlowItem{})
			},
			want: ErrInvalidFlowReport,
		},
		{
			name: "gost report from nft mode",
			fn: func(tx *gorm.DB) error {
				return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, dto.FlowDto{
					N: fmt.Sprintf("%d_%d_0", fixture.forward.ID, fixture.user.ID), U: 1, D: 1,
				})
			},
			want: ErrFlowNodeMismatch,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := model.DB.Transaction(tc.fn)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestApplyGostFlowDatabaseFailureRollsBackAllCounters(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	trigger := fmt.Sprintf(`
CREATE TRIGGER fail_flow_user_update
BEFORE UPDATE OF in_flow, out_flow ON user
WHEN OLD.id = %d
BEGIN
  SELECT RAISE(FAIL, 'injected user flow update failure');
END`, fixture.user.ID)
	if err := model.DB.Exec(trigger).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	flow := dto.FlowDto{
		N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID), U: 100, D: 200,
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
	})
	if err == nil || !strings.Contains(err.Error(), "injected user flow update failure") {
		t.Fatalf("err=%v, want injected DB update failure", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	user := loadFlowUser(t, fixture.user.ID)
	userTunnel := loadFlowUserTunnel(t, fixture.userTunnel.ID)
	for label, counters := range map[string][2]int64{
		"forward":     {forward.InFlow, forward.OutFlow},
		"user":        {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{} {
			t.Fatalf("%s counters=%v, want rollback to zero", label, counters)
		}
	}
}

func nftFlowItem(forwardID, userID, userTunnelID, up, down int64) dto.NftFlowItem {
	return dto.NftFlowItem{
		ForwardID: &forwardID, UserID: &userID, UserTunnelID: &userTunnelID, Up: &up, Down: &down,
	}
}

func loadFlowForward(t *testing.T, id int64) model.Forward {
	t.Helper()
	var forward model.Forward
	if err := model.DB.First(&forward, id).Error; err != nil {
		t.Fatalf("load forward: %v", err)
	}
	return forward
}

func loadFlowUser(t *testing.T, id int64) model.User {
	t.Helper()
	var user model.User
	if err := model.DB.First(&user, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return user
}

func loadFlowUserTunnel(t *testing.T, id int64) model.UserTunnel {
	t.Helper()
	var userTunnel model.UserTunnel
	if err := model.DB.First(&userTunnel, id).Error; err != nil {
		t.Fatalf("load user tunnel: %v", err)
	}
	return userTunnel
}
