package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestNormalizePackageTrafficRange(t *testing.T) {
	cases := map[string]string{
		"":      "24h",
		"24H":   "24h",
		"7d":    "7d",
		"30D":   "30d",
		"weird": "24h",
	}

	for in, want := range cases {
		if got := normalizePackageTrafficRange(in); got != want {
			t.Fatalf("normalizePackageTrafficRange(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestFlowStatisticsUseExactHourlyAndDailyBuckets(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Date(2026, 8, 2, 12, 34, 0, 0, time.Local)
	yesterday := startOfLocalDay(now).AddDate(0, 0, -1)
	rows := []model.TrafficHourly{
		trafficHourlyFixture(7, yesterday.Add(9*time.Hour), 1, 2),
		trafficHourlyFixture(7, yesterday.Add(10*time.Hour), 3, 4),
		trafficHourlyFixture(7, startOfLocalHour(now), 5, 6),
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatalf("create traffic rows: %v", err)
	}

	hourly, err := getFlowStatisticsForScopeAt(packageTrafficScope{UserID: 7}, "24h", now)
	if err != nil {
		t.Fatalf("get 24h statistics: %v", err)
	}
	if len(hourly) != 24 {
		t.Fatalf("24h range should return 24 points, got %d", len(hourly))
	}
	for i, row := range hourly {
		wantBucket := startOfLocalHour(now).Add(-time.Duration(i) * time.Hour)
		if row.CreatedTime != wantBucket.UnixMilli() || row.Time != wantBucket.Format("15:00") {
			t.Fatalf("hour bucket %d=%#v, want %s", i, row, wantBucket)
		}
	}
	if hourly[0].InFlow != 5 || hourly[0].OutFlow != 6 || hourly[0].Flow != 11 {
		t.Fatalf("current hour not aggregated correctly: %#v", hourly[0])
	}

	sevenDays, err := getFlowStatisticsForScopeAt(packageTrafficScope{UserID: 7}, "7d", now)
	if err != nil {
		t.Fatalf("get 7d statistics: %v", err)
	}
	if len(sevenDays) != 7 {
		t.Fatalf("7d range should return 7 points, got %d", len(sevenDays))
	}
	if sevenDays[5].InFlow != 4 || sevenDays[5].OutFlow != 6 || sevenDays[5].Flow != 10 {
		t.Fatalf("yesterday bucket not aggregated correctly: %#v", sevenDays[5])
	}
	if sevenDays[6].InFlow != 5 || sevenDays[6].OutFlow != 6 || sevenDays[6].Flow != 11 {
		t.Fatalf("today bucket not aggregated correctly: %#v", sevenDays[6])
	}

	thirtyDays, err := getFlowStatisticsForScopeAt(packageTrafficScope{UserID: 7}, "30d", now)
	if err != nil {
		t.Fatalf("get 30d statistics: %v", err)
	}
	if len(thirtyDays) != 30 {
		t.Fatalf("30d range should return 30 points, got %d", len(thirtyDays))
	}
}

func TestStartOfLocalHourKeepsRepeatedDSTHoursDistinct(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("load DST timezone: %v", err)
	}
	originalLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = originalLocal })

	first := startOfLocalHour(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC))
	second := startOfLocalHour(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC))
	if second.Sub(first) != time.Hour || first.Hour() != 1 || second.Hour() != 1 {
		t.Fatalf("repeated hours were not preserved: first=%s second=%s", first, second)
	}
}

func TestTodayTrafficDoesNotIncludePreviousDayOrDisappearAfterReset(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Date(2026, 8, 2, 0, 10, 0, 0, time.Local)
	today := startOfLocalDay(now)
	rows := []model.TrafficHourly{
		trafficHourlyFixture(1, today.Add(-time.Hour), 100, 200),
		trafficHourlyFixture(1, today, 3, 4),
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatalf("create traffic rows: %v", err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).
		Updates(map[string]interface{}{"in_flow": 999, "out_flow": 888}).Error; err != nil {
		t.Fatalf("seed cumulative counters: %v", err)
	}

	before, err := getTrafficTotalsForWindow(packageTrafficScope{UserID: 1}, today, today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("get today traffic: %v", err)
	}
	if before != (packageTrafficTotals{InFlow: 3, OutFlow: 4, TotalFlow: 7}) {
		t.Fatalf("today traffic crossed midnight: %#v", before)
	}

	if res := ResetFlow(dto.ResetFlowDto{ID: 1, Type: 1}); res.Code != 0 {
		t.Fatalf("reset flow: %+v", res)
	}
	after, err := getTrafficTotalsForWindow(packageTrafficScope{UserID: 1}, today, today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("get today traffic after reset: %v", err)
	}
	if after != before {
		t.Fatalf("reset changed today's immutable increments: before=%#v after=%#v", before, after)
	}
}

func TestAdministratorDashboardUsesSystemTrafficScope(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Now()
	bucket := time.UnixMilli(model.TrafficHourlyBucketStart(now))
	expires := now.Add(24 * time.Hour).UnixMilli()
	regular := model.User{
		ID: 20, User: "dashboard-user", Pwd: "unused", RoleID: userRoleID,
		ExpTime: &expires, Flow: 100, InFlow: 11, OutFlow: 22,
		FlowResetTime: now.UnixMilli(), Num: 1, CreatedTime: now.UnixMilli(), Status: userStatusActive,
	}
	if err := model.DB.Create(&regular).Error; err != nil {
		t.Fatalf("create regular user: %v", err)
	}
	ledger := []model.TrafficHourly{
		trafficHourlyFixture(20, bucket, 10, 20),
		trafficHourlyFixture(21, bucket, 30, 40),
	}
	if err := model.DB.Create(&ledger).Error; err != nil {
		t.Fatalf("create system ledger: %v", err)
	}
	forwards := []model.Forward{
		{UserID: 20, UserName: "a", Name: "a", TunnelID: 1, InPort: 10001, RemoteAddr: "192.0.2.1:80", InFlow: 11, OutFlow: 22, CreatedTime: now.UnixMilli(), UpdatedTime: now.UnixMilli(), Status: 1},
		{UserID: 21, UserName: "b", Name: "b", TunnelID: 1, InPort: 10002, RemoteAddr: "192.0.2.2:80", InFlow: 33, OutFlow: 44, CreatedTime: now.UnixMilli(), UpdatedTime: now.UnixMilli(), Status: 1},
	}
	if err := model.DB.Create(&forwards).Error; err != nil {
		t.Fatalf("create forwards: %v", err)
	}

	res := GetUserPackageInfo(1, "24h", nil)
	if res.Code != 0 {
		t.Fatalf("get admin package: %+v", res)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("package data type=%T", res.Data)
	}
	if data["trafficScope"] != "system" {
		t.Fatalf("trafficScope=%#v", data["trafficScope"])
	}
	if got := data["trafficTotals"].(packageTrafficTotals); got != (packageTrafficTotals{InFlow: 44, OutFlow: 66, TotalFlow: 110}) {
		t.Fatalf("system cumulative totals=%#v", got)
	}
	if got := data["todayTraffic"].(packageTrafficTotals); got != (packageTrafficTotals{InFlow: 40, OutFlow: 60, TotalFlow: 100}) {
		t.Fatalf("system today totals=%#v", got)
	}
	statistics := data["statisticsFlows"].([]model.StatisticsFlow)
	if len(statistics) != 24 || statistics[0].InFlow != 40 || statistics[0].OutFlow != 60 {
		t.Fatalf("system trend current bucket=%#v", statistics)
	}

	regularRes := GetUserPackageInfo(regular.ID, "24h", nil)
	if regularRes.Code != 0 {
		t.Fatalf("get regular package: %+v", regularRes)
	}
	regularData := regularRes.Data.(map[string]interface{})
	if regularData["trafficScope"] != "user" {
		t.Fatalf("regular trafficScope=%#v", regularData["trafficScope"])
	}
	if got := regularData["todayTraffic"].(packageTrafficTotals); got != (packageTrafficTotals{InFlow: 10, OutFlow: 20, TotalFlow: 30}) {
		t.Fatalf("regular today totals leaked system traffic: %#v", got)
	}
	regularStatistics := regularData["statisticsFlows"].([]model.StatisticsFlow)
	if regularStatistics[0].InFlow != 10 || regularStatistics[0].OutFlow != 20 {
		t.Fatalf("regular trend leaked system traffic: %#v", regularStatistics[0])
	}
}

func TestTunnelFlowStatisticsUseTunnelLedgerForAllRanges(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Date(2026, 8, 8, 12, 34, 0, 0, time.Local)
	today := startOfLocalDay(now)
	yesterday := today.AddDate(0, 0, -1)
	tunnelID := int64(11)
	rows := []model.TrafficTunnelHourly{
		trafficTunnelHourlyFixture(7, tunnelID, yesterday.Add(9*time.Hour), 1, 2),
		trafficTunnelHourlyFixture(8, tunnelID, yesterday.Add(10*time.Hour), 3, 4),
		trafficTunnelHourlyFixture(7, tunnelID, startOfLocalHour(now), 5, 6),
		trafficTunnelHourlyFixture(7, 12, startOfLocalHour(now), 100, 200),
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatalf("create tunnel traffic rows: %v", err)
	}

	adminScope := packageTrafficScope{UserID: 1, AllUsers: true, TunnelID: &tunnelID}
	hourly, err := getFlowStatisticsForScopeAt(adminScope, "24h", now)
	if err != nil {
		t.Fatalf("get tunnel 24h statistics: %v", err)
	}
	if hourly[0].InFlow != 5 || hourly[0].OutFlow != 6 {
		t.Fatalf("tunnel current hour included another tunnel: %#v", hourly[0])
	}

	sevenDays, err := getFlowStatisticsForScopeAt(adminScope, "7d", now)
	if err != nil {
		t.Fatalf("get tunnel 7d statistics: %v", err)
	}
	if sevenDays[5].InFlow != 4 || sevenDays[5].OutFlow != 6 || sevenDays[6].Flow != 11 {
		t.Fatalf("tunnel 7d aggregation=%#v", sevenDays)
	}

	thirtyDays, err := getFlowStatisticsForScopeAt(adminScope, "30d", now)
	if err != nil {
		t.Fatalf("get tunnel 30d statistics: %v", err)
	}
	if len(thirtyDays) != 30 || thirtyDays[28].Flow != 10 || thirtyDays[29].Flow != 11 {
		t.Fatalf("tunnel 30d aggregation=%#v", thirtyDays)
	}

	userScope := packageTrafficScope{UserID: 7, TunnelID: &tunnelID}
	userDaily, err := getFlowStatisticsForScopeAt(userScope, "7d", now)
	if err != nil {
		t.Fatalf("get user tunnel statistics: %v", err)
	}
	if userDaily[5].InFlow != 1 || userDaily[5].OutFlow != 2 {
		t.Fatalf("user tunnel trend leaked another user: %#v", userDaily[5])
	}
}

func TestUserPackageTunnelSelectionEnforcesScopeAndReturnsRoute(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Now()
	nowMs := now.UnixMilli()
	expires := now.Add(24 * time.Hour).UnixMilli()
	users := []model.User{
		{User: "alice-tunnel", Pwd: "unused", RoleID: userRoleID, ExpTime: &expires, Flow: 100, FlowResetTime: nowMs, Num: 1, CreatedTime: nowMs, Status: userStatusActive},
		{User: "bob-tunnel", Pwd: "unused", RoleID: userRoleID, ExpTime: &expires, Flow: 100, FlowResetTime: nowMs, Num: 1, CreatedTime: nowMs, Status: userStatusActive},
	}
	if err := model.DB.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	nodes := []model.Node{
		{Name: "A", Secret: "traffic-a", IP: "192.0.2.1", ServerIP: "192.0.2.1", PortSta: 10000, PortEnd: 10999, ForwardMode: forwardModeNftables, CreatedTime: nowMs, Status: 1},
		{Name: "B", Secret: "traffic-b", IP: "192.0.2.2", ServerIP: "192.0.2.2", PortSta: 11000, PortEnd: 11999, ForwardMode: forwardModeNftables, CreatedTime: nowMs, Status: 1},
		{Name: "C", Secret: "traffic-c", IP: "192.0.2.3", ServerIP: "192.0.2.3", PortSta: 12000, PortEnd: 12999, ForwardMode: forwardModeNftables, CreatedTime: nowMs, Status: 1},
	}
	if err := model.DB.Create(&nodes).Error; err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	protocol := "tcp"
	tunnels := []model.Tunnel{
		{Name: "主线路", InNodeID: nodes[0].ID, InIP: nodes[0].IP, OutNodeID: nodes[1].ID, OutIP: nodes[1].IP, Type: tunnelTypeTunnelForward, Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: nowMs, UpdatedTime: nowMs, Status: 1},
		{Name: "备用线路", InNodeID: nodes[0].ID, InIP: nodes[0].IP, OutNodeID: nodes[2].ID, OutIP: nodes[2].IP, Type: tunnelTypeTunnelForward, Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: nowMs, UpdatedTime: nowMs, Status: 1},
	}
	if err := model.DB.Create(&tunnels).Error; err != nil {
		t.Fatalf("create tunnels: %v", err)
	}
	permissions := []model.UserTunnel{
		{UserID: users[0].ID, TunnelID: tunnels[0].ID, Num: 1, Flow: 100, FlowResetTime: nowMs, ExpTime: &expires, Status: 1},
		{UserID: users[1].ID, TunnelID: tunnels[0].ID, Num: 1, Flow: 100, FlowResetTime: nowMs, ExpTime: &expires, Status: 1},
	}
	if err := model.DB.Create(&permissions).Error; err != nil {
		t.Fatalf("create tunnel permissions: %v", err)
	}
	bucket := startOfLocalHour(now)
	ledger := []model.TrafficTunnelHourly{
		trafficTunnelHourlyFixture(users[0].ID, tunnels[0].ID, bucket, 10, 20),
		trafficTunnelHourlyFixture(users[1].ID, tunnels[0].ID, bucket, 30, 40),
		trafficTunnelHourlyFixture(users[0].ID, tunnels[1].ID, bucket, 100, 200),
	}
	if err := model.DB.Create(&ledger).Error; err != nil {
		t.Fatalf("create tunnel ledger: %v", err)
	}

	selectedID := tunnels[0].ID
	adminRes := GetUserPackageInfo(1, "24h", &selectedID)
	if adminRes.Code != 0 {
		t.Fatalf("get admin tunnel package: %+v", adminRes)
	}
	adminData := adminRes.Data.(map[string]interface{})
	adminTrend := adminData["statisticsFlows"].([]model.StatisticsFlow)
	if adminTrend[0].InFlow != 40 || adminTrend[0].OutFlow != 60 {
		t.Fatalf("admin selected tunnel trend=%#v", adminTrend[0])
	}
	if got := adminData["trafficTrendTotals"].(packageTrafficTotals); got != (packageTrafficTotals{InFlow: 40, OutFlow: 60, TotalFlow: 100}) {
		t.Fatalf("admin selected tunnel total=%#v", got)
	}
	selected := adminData["trafficTunnel"].(*dto.TrafficTunnelOption)
	if selected.InNodeName != "A" || selected.OutNodeName != "B" {
		t.Fatalf("selected route=%#v", selected)
	}

	userRes := GetUserPackageInfo(users[0].ID, "24h", &selectedID)
	if userRes.Code != 0 {
		t.Fatalf("get user tunnel package: %+v", userRes)
	}
	userData := userRes.Data.(map[string]interface{})
	userTrend := userData["statisticsFlows"].([]model.StatisticsFlow)
	if userTrend[0].InFlow != 10 || userTrend[0].OutFlow != 20 {
		t.Fatalf("user selected tunnel leaked another user: %#v", userTrend[0])
	}
	options := userData["trafficTunnels"].([]dto.TrafficTunnelOption)
	if len(options) != 1 || options[0].TunnelID != selectedID {
		t.Fatalf("user tunnel options=%#v", options)
	}

	unauthorizedID := tunnels[1].ID
	if unauthorized := GetUserPackageInfo(users[0].ID, "24h", &unauthorizedID); unauthorized.Code == 0 {
		t.Fatalf("user could query unauthorized tunnel: %+v", unauthorized)
	}
}

func TestDeleteTunnelRemovesItsTrafficLedger(t *testing.T) {
	initUserPackageTestDB(t)
	now := time.Now()
	nowMs := now.UnixMilli()
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name: "retired-route", InNodeID: 101, InIP: "192.0.2.101", OutNodeID: 102,
		OutIP: "192.0.2.102", Type: tunnelTypeTunnelForward, Protocol: &protocol, Flow: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: nowMs,
		UpdatedTime: nowMs, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	row := trafficTunnelHourlyFixture(1, tunnel.ID, startOfLocalHour(now), 10, 20)
	if err := model.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if res := DeleteTunnel(tunnel.ID); res.Code != 0 {
		t.Fatalf("delete tunnel: %+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.TrafficTunnelHourly{}).Where("tunnel_id = ?", tunnel.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deleted tunnel traffic rows=%d", count)
	}
}

func initUserPackageTestDB(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
}

func trafficHourlyFixture(userID int64, bucket time.Time, inFlow, outFlow int64) model.TrafficHourly {
	stamp := bucket.UnixMilli()
	return model.TrafficHourly{
		UserID: userID, BucketStart: stamp, InFlow: inFlow, OutFlow: outFlow,
		CreatedTime: stamp, UpdatedTime: stamp,
	}
}

func trafficTunnelHourlyFixture(userID, tunnelID int64, bucket time.Time, inFlow, outFlow int64) model.TrafficTunnelHourly {
	stamp := bucket.UnixMilli()
	return model.TrafficTunnelHourly{
		UserID: userID, TunnelID: tunnelID, BucketStart: stamp, InFlow: inFlow, OutFlow: outFlow,
		CreatedTime: stamp, UpdatedTime: stamp,
	}
}
