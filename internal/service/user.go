package service

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	adminRoleID        = 0
	userRoleID         = 1
	userStatusDisabled = model.UserStatusDisabled
	userStatusActive   = model.UserStatusActive
	// 默认管理员账号名/密码仅用于首登识别（不作为鉴权依据）
	defaultUsername = "admin_user"
	bytesToGB       = int64(1024 * 1024 * 1024)

	// 登录失败锁定策略
	maxLoginFailCount = 5                // 连续失败 5 次
	loginLockDuration = 15 * time.Minute // 锁定 15 分钟
)

func isActiveUserStatus(status int) bool {
	return model.IsActiveUserStatus(status)
}

func isValidUserStatus(status int) bool {
	return status == userStatusDisabled || status == userStatusActive
}

// Login 登录（Go 版当前未接入验证码校验）。
func Login(req dto.LoginDto, clientIP string) result.R {
	user, err := AuthenticateCredentials(req.Username, req.Password, clientIP)
	if err != nil {
		switch {
		case errors.Is(err, ErrAttemptLimited):
			return result.Err("登录尝试过多，请稍后重试")
		case errors.Is(err, ErrAccountDisabled):
			return result.Err("账号已被禁用")
		case errors.Is(err, ErrAccountExpired):
			return result.Err("账号已过期")
		case errors.Is(err, ErrCredentialStore):
			return result.Err("认证服务暂不可用，请稍后重试")
		default:
			// 用户不存在与密码错误使用统一文案，避免用户名枚举。
			return result.Err("账号或密码错误")
		}
	}

	// 历史 MD5 密码登录成功后升级为 bcrypt；MD5 账号同时要求强制改密（后端硬阻断）
	needChangeDueToMd5 := !crypto.IsBcryptHash(user.Pwd)
	if needChangeDueToMd5 {
		if hashed, err := crypto.HashPassword(req.Password); err == nil {
			// 同时升级密码哈希并置 must_change_pwd=1，触发后端强制改密中间件
			model.DB.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
				"pwd":             hashed,
				"must_change_pwd": 1,
			})
		}
	}

	token, err := crypto.GenerateToken(user.ID, user.User, user.RoleID, user.TokenVersion)
	if err != nil {
		return result.Err("生成令牌失败")
	}
	requirePasswordChange := user.MustChangePwd == 1 || needChangeDueToMd5
	return result.Ok(map[string]interface{}{
		"token":                 token,
		"name":                  user.User,
		"role_id":               user.RoleID,
		"requirePasswordChange": requirePasswordChange,
	})
}

// CreateUser 创建用户（管理员）
func CreateUser(req dto.UserDto) result.R {
	var exist model.User
	if err := model.DB.Where("user = ?", req.User).First(&exist).Error; err == nil {
		return result.Err("用户名已存在")
	}

	status := 1
	if req.Status != nil {
		if !isValidUserStatus(*req.Status) {
			return result.Err("用户状态参数错误")
		}
		status = *req.Status
	}
	hashedPwd, err := crypto.HashPassword(req.Pwd)
	if err != nil {
		return result.Err("密码加密失败")
	}
	now := time.Now().UnixMilli()
	user := model.User{
		User:          req.User,
		Pwd:           hashedPwd,
		RoleID:        userRoleID,
		ExpTime:       &req.ExpTime,
		Flow:          req.Flow,
		FlowResetTime: req.FlowResetTime,
		Num:           req.Num,
		CreatedTime:   now,
		UpdatedTime:   &now,
		Status:        status,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		return result.Err("用户创建失败")
	}
	return result.OkMsg("用户创建成功")
}

// GetAllUsers 用户列表（排除管理员）
func GetAllUsers() result.R {
	var users []model.User
	model.DB.Where("role_id <> ?", adminRoleID).Find(&users)
	return result.Ok(users)
}

// UpdateUser 更新用户（管理员）
func UpdateUser(req dto.UserUpdateDto) result.R {
	var user model.User
	if err := model.DB.First(&user, req.ID).Error; err != nil {
		return result.Err("用户不存在")
	}
	if user.RoleID == adminRoleID {
		return result.Err("不能修改管理员账户")
	}
	if req.Status != nil && !isValidUserStatus(*req.Status) {
		return result.Err("用户状态参数错误")
	}

	var dup model.User
	if err := model.DB.Where("user = ? AND id <> ?", req.User, req.ID).First(&dup).Error; err == nil {
		return result.Err("用户名已被占用")
	}

	updates := map[string]interface{}{
		"user":            req.User,
		"flow":            req.Flow,
		"num":             req.Num,
		"exp_time":        req.ExpTime,
		"flow_reset_time": req.FlowResetTime,
		"updated_time":    time.Now().UnixMilli(),
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Pwd != "" {
		hashedPwd, err := crypto.HashPassword(req.Pwd)
		if err != nil {
			return result.Err("密码加密失败")
		}
		updates["pwd"] = hashedPwd
		updates["token_version"] = gorm.Expr("token_version + 1")
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", req.ID).Updates(updates).Error; err != nil {
			return err
		}
		// Username is denormalized onto forward.user_name; propagate it in the
		// same transaction so no forward keeps a stale owner name.
		if req.User != user.User {
			if err := tx.Model(&model.Forward{}).Where("user_id = ?", req.ID).
				Update("user_name", req.User).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return result.Err("用户更新失败")
	}
	return result.OkMsg("用户更新成功")
}

// DeleteUser 删除用户，级联删除转发/Gost 服务/隧道权限/统计
func DeleteUser(id int64) result.R {
	var user model.User
	if err := model.DB.First(&user, id).Error; err != nil {
		return result.Err("用户不存在")
	}
	if user.RoleID == adminRoleID {
		return result.Err("不能删除管理员账户")
	}

	// 先删除用户的转发对应 Gost 服务（网络操作，在事务外执行——不可回滚）
	var forwards []model.Forward
	if err := model.DB.Where("user_id = ?", id).Find(&forwards).Error; err != nil {
		return result.Err("用户删除失败")
	}
	forwardIDs := make([]int64, 0, len(forwards))
	for _, f := range forwards {
		forwardIDs = append(forwardIDs, f.ID)
		deleteGostServicesForForward(&f, id)
	}

	// 事务包裹 DB 级联删除，避免中途失败留脏数据
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := deleteForwardRowsByIDs(tx, forwardIDs); err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.UserTunnel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.StatisticsFlow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.TrafficHourly{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.TrafficTunnelHourly{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.User{}, id).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return result.Err("用户删除失败")
	}
	return result.OkMsg("用户删除成功")
}

// deleteGostServicesForForward 删除单条转发对应的 gost 服务（尽力而为）
func deleteGostServicesForForward(forward *model.Forward, userID int64) {
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
		return
	}
	f := *forward
	if f.UserID == 0 {
		f.UserID = userID
	}
	deleteOldGostServices(&f, &tunnel)
}

// UpdatePassword 修改账号密码（当前登录用户）
func UpdatePassword(userID int64, req dto.ChangePasswordDto) result.R {
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		return result.Err("用户不存在")
	}
	if req.NewPassword != req.ConfirmPassword {
		return result.Err("两次输入的密码不一致")
	}
	if !crypto.VerifyPassword(user.Pwd, req.CurrentPassword) {
		return result.Err("当前密码错误")
	}
	if user.User != req.NewUsername {
		var dup model.User
		if err := model.DB.Where("user = ? AND id <> ?", req.NewUsername, userID).First(&dup).Error; err == nil {
			return result.Err("用户名已被占用")
		}
	}
	hashedPwd, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		return result.Err("密码加密失败")
	}
	updates := map[string]interface{}{
		"user":             req.NewUsername,
		"pwd":              hashedPwd,
		"updated_time":     time.Now().UnixMilli(),
		"must_change_pwd":  0, // 改密成功后清除强制改密标志
		"login_fail_count": 0,
		"token_version":    gorm.Expr("token_version + 1"),
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			return err
		}
		// 用户名冗余在 forward.user_name，同一事务内一并传播，避免遗留旧名。
		if user.User != req.NewUsername {
			if err := tx.Model(&model.Forward{}).Where("user_id = ?", userID).
				Update("user_name", req.NewUsername).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return result.Err("修改失败")
	}
	return result.OkMsg("修改成功")
}

// ResetFlow 清零流量：type=1 账号流量，否则用户隧道流量
func ResetFlow(req dto.ResetFlowDto) result.R {
	if req.Type == 1 {
		var user model.User
		if err := model.DB.First(&user, req.ID).Error; err != nil {
			return result.Err("用户不存在")
		}
		model.DB.Model(&model.User{}).Where("id = ?", req.ID).
			Updates(map[string]interface{}{"in_flow": 0, "out_flow": 0})
	} else {
		var ut model.UserTunnel
		if err := model.DB.First(&ut, req.ID).Error; err != nil {
			return result.Err("隧道不存在")
		}
		model.DB.Model(&model.UserTunnel{}).Where("id = ?", req.ID).
			Updates(map[string]interface{}{"in_flow": 0, "out_flow": 0})
	}
	return result.OkEmpty()
}

// GetUserPackageInfo 用户套餐信息（dashboard 数据源）。
// trafficTunnelID 为空时沿用用户/全系统趋势；指定后仅查询已授权的单条隧道。
func GetUserPackageInfo(userID int64, trafficRange string, trafficTunnelID *int64) result.R {
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		return result.Err("用户不存在")
	}
	baseScope := packageTrafficScope{UserID: userID, AllUsers: user.RoleID == adminRoleID}
	trafficTunnels, err := loadTrafficTunnelOptions(baseScope)
	if err != nil {
		return result.Err("读取流量隧道失败")
	}
	selectedTrafficTunnel, ok := selectTrafficTunnel(trafficTunnels, trafficTunnelID)
	if !ok {
		return result.Err("隧道不存在或无权查看")
	}

	// 用户基本信息（密码不返回）
	userInfo := map[string]interface{}{
		"id":            user.ID,
		"name":          nil,
		"user":          user.User,
		"status":        user.Status,
		"flow":          user.Flow,
		"inFlow":        user.InFlow,
		"outFlow":       user.OutFlow,
		"num":           user.Num,
		"expTime":       user.ExpTime,
		"flowResetTime": user.FlowResetTime,
		"createdTime":   user.CreatedTime,
		"updatedTime":   user.UpdatedTime,
	}

	// 隧道权限详情（连表）
	var tunnelPermissions []dto.UserTunnelDetail
	model.DB.Raw(`
		SELECT
			ut.id, ut.user_id AS user_id, ut.tunnel_id AS tunnel_id,
			t.name AS tunnel_name, t.flow AS tunnel_flow,
			ut.flow, ut.in_flow AS in_flow, ut.out_flow AS out_flow,
			ut.num, ut.flow_reset_time AS flow_reset_time, ut.exp_time AS exp_time,
			ut.speed_id AS speed_id, sl.name AS speed_limit_name, sl.speed
		FROM user_tunnel ut
		LEFT JOIN tunnel t ON ut.tunnel_id = t.id
		LEFT JOIN speed_limit sl ON ut.speed_id = sl.id
		WHERE ut.user_id = ?
		ORDER BY ut.id`, userID).Scan(&tunnelPermissions)

	// 转发详情（连表）
	var forwards []dto.UserForwardDetail
	model.DB.Raw(`
		SELECT
			f.id, f.name, f.tunnel_id AS tunnel_id, t.name AS tunnel_name,
			t.in_ip AS in_ip, f.in_port AS in_port, f.remote_addr AS remote_addr,
			f.in_flow AS in_flow, f.out_flow AS out_flow, f.status,
			f.created_time AS created_time
		FROM forward f
		LEFT JOIN tunnel t ON f.tunnel_id = t.id
		WHERE f.user_id = ?
		ORDER BY f.created_time DESC`, userID).Scan(&forwards)

	normalizedRange := normalizePackageTrafficRange(trafficRange)
	trendScope := baseScope
	trendScope.TunnelID = trafficTunnelID
	now := time.Now()
	statisticsFlows, err := getFlowStatisticsForScopeAt(trendScope, normalizedRange, now)
	if err != nil {
		return result.Err("读取流量趋势失败")
	}
	todayTraffic, err := getTrafficTotalsForWindow(baseScope, startOfLocalDay(now), startOfLocalDay(now).AddDate(0, 0, 1))
	if err != nil {
		return result.Err("读取今日流量失败")
	}
	trafficTotals, err := getPackageTrafficTotals(user, baseScope)
	if err != nil {
		return result.Err("读取累计流量失败")
	}

	if tunnelPermissions == nil {
		tunnelPermissions = []dto.UserTunnelDetail{}
	}
	if forwards == nil {
		forwards = []dto.UserForwardDetail{}
	}
	if trafficTunnels == nil {
		trafficTunnels = []dto.TrafficTunnelOption{}
	}

	return result.Ok(map[string]interface{}{
		"userInfo":           userInfo,
		"tunnelPermissions":  tunnelPermissions,
		"forwards":           forwards,
		"trafficRange":       normalizedRange,
		"trafficScope":       baseScope.Name(),
		"trafficTunnelId":    trafficTunnelID,
		"trafficTunnel":      selectedTrafficTunnel,
		"trafficTunnels":     trafficTunnels,
		"trafficTotals":      trafficTotals,
		"todayTraffic":       todayTraffic,
		"trafficTrendTotals": trafficTotalsFromStatistics(statisticsFlows),
		"statisticsFlows":    statisticsFlows,
	})
}

func loadTrafficTunnelOptions(scope packageTrafficScope) ([]dto.TrafficTunnelOption, error) {
	const selectSQL = `
		SELECT
			t.id AS tunnel_id, t.name AS tunnel_name, t.type,
			t.in_node_id AS in_node_id, COALESCE(in_node.name, '') AS in_node_name,
			t.out_node_id AS out_node_id, COALESCE(out_node.name, '') AS out_node_name
		FROM tunnel t
		LEFT JOIN node in_node ON in_node.id = t.in_node_id
		LEFT JOIN node out_node ON out_node.id = t.out_node_id`
	var options []dto.TrafficTunnelOption
	query := selectSQL
	args := []interface{}{}
	if !scope.AllUsers {
		query += `
		WHERE EXISTS (
			SELECT 1 FROM user_tunnel ut
			WHERE ut.user_id = ? AND ut.tunnel_id = t.id
		) OR EXISTS (
			SELECT 1 FROM forward f
			WHERE f.user_id = ? AND f.tunnel_id = t.id
		)`
		args = append(args, scope.UserID, scope.UserID)
	}
	query += " ORDER BY t.name ASC, t.id ASC"
	if err := model.DB.Raw(query, args...).Scan(&options).Error; err != nil {
		return nil, err
	}
	return options, nil
}

func selectTrafficTunnel(options []dto.TrafficTunnelOption, tunnelID *int64) (*dto.TrafficTunnelOption, bool) {
	if tunnelID == nil {
		return nil, true
	}
	if *tunnelID <= 0 {
		return nil, false
	}
	for i := range options {
		if options[i].TunnelID == *tunnelID {
			return &options[i], true
		}
	}
	return nil, false
}

// packageTrafficScope 统一控制台 KPI、今日流量与趋势图的统计范围。
// 管理员查看全系统，普通账号只查看自己的计费流量，避免累计值与趋势图口径不一致。
type packageTrafficScope struct {
	UserID   int64
	AllUsers bool
	TunnelID *int64
}

func (scope packageTrafficScope) Name() string {
	if scope.AllUsers {
		return "system"
	}
	return "user"
}

type packageTrafficTotals struct {
	InFlow    int64 `json:"inFlow"`
	OutFlow   int64 `json:"outFlow"`
	TotalFlow int64 `json:"totalFlow"`
}

type hourlyTrafficSum struct {
	BucketStart int64 `gorm:"column:bucket_start"`
	InFlow      int64 `gorm:"column:in_flow"`
	OutFlow     int64 `gorm:"column:out_flow"`
}

func getPackageTrafficTotals(user model.User, scope packageTrafficScope) (packageTrafficTotals, error) {
	if !scope.AllUsers {
		return newPackageTrafficTotals(user.InFlow, user.OutFlow), nil
	}

	// 管理员累计流量沿用“当前全部转发计数器之和”的控制台口径。
	// 今日流量与趋势则来自不会被套餐清零破坏的小时增量账本。
	var sums struct {
		InFlow  int64 `gorm:"column:in_flow"`
		OutFlow int64 `gorm:"column:out_flow"`
	}
	err := model.DB.Model(&model.Forward{}).
		Select("COALESCE(SUM(in_flow), 0) AS in_flow, COALESCE(SUM(out_flow), 0) AS out_flow").
		Scan(&sums).Error
	return newPackageTrafficTotals(sums.InFlow, sums.OutFlow), err
}

func newPackageTrafficTotals(inFlow, outFlow int64) packageTrafficTotals {
	return packageTrafficTotals{
		InFlow:    inFlow,
		OutFlow:   outFlow,
		TotalFlow: totalFlowBytes(inFlow, outFlow),
	}
}

func normalizePackageTrafficRange(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "7d":
		return "7d"
	case "30d":
		return "30d"
	default:
		return "24h"
	}
}

func getFlowStatisticsForScopeAt(scope packageTrafficScope, trafficRange string, now time.Time) ([]model.StatisticsFlow, error) {
	now = now.In(time.Local)
	switch normalizePackageTrafficRange(trafficRange) {
	case "7d":
		return getDailyFlowStatisticsAt(scope, 7, now)
	case "30d":
		return getDailyFlowStatisticsAt(scope, 30, now)
	default:
		return getLast24HoursFlowStatisticsAt(scope, now)
	}
}

// getLast24HoursFlowStatisticsAt 返回包含当前小时在内的 24 个真实小时桶。
// 结果保持“最新在前”的历史 API 顺序，缺失小时固定补零，不再让重复快照挤掉有效数据。
func getLast24HoursFlowStatisticsAt(scope packageTrafficScope, now time.Time) ([]model.StatisticsFlow, error) {
	currentHour := startOfLocalHour(now)
	firstHour := currentHour.Add(-23 * time.Hour)
	sums, err := loadHourlyTrafficSums(scope, firstHour, currentHour.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	byBucket := make(map[int64]hourlyTrafficSum, len(sums))
	for _, sum := range sums {
		byBucket[sum.BucketStart] = sum
	}

	rows := make([]model.StatisticsFlow, 0, 24)
	for i := 0; i < 24; i++ {
		bucket := currentHour.Add(-time.Duration(i) * time.Hour)
		sum := byBucket[bucket.UnixMilli()]
		rows = append(rows, statisticsFlowFromSum(scope, bucket.Format("15:00"), bucket.UnixMilli(), sum.InFlow, sum.OutFlow))
	}
	return rows, nil
}

func getDailyFlowStatisticsAt(scope packageTrafficScope, days int, now time.Time) ([]model.StatisticsFlow, error) {
	if days <= 0 {
		days = 7
	}
	today := startOfLocalDay(now)
	start := today.AddDate(0, 0, -(days - 1))
	end := today.AddDate(0, 0, 1)

	buckets := make([]model.StatisticsFlow, days)
	for i := range buckets {
		day := start.AddDate(0, 0, i)
		buckets[i] = model.StatisticsFlow{
			UserID:      scope.UserID,
			Time:        day.Format("01-02"),
			CreatedTime: day.UnixMilli(),
		}
	}

	sums, err := loadHourlyTrafficSums(scope, start, end)
	if err != nil {
		return nil, err
	}
	for _, sum := range sums {
		bucketTime := time.UnixMilli(sum.BucketStart).In(time.Local)
		day := startOfLocalDay(bucketTime)
		index := calendarDayOffset(start, day)
		if index < 0 || index >= len(buckets) {
			continue
		}
		buckets[index].InFlow = totalFlowBytes(buckets[index].InFlow, sum.InFlow)
		buckets[index].OutFlow = totalFlowBytes(buckets[index].OutFlow, sum.OutFlow)
		buckets[index].Flow = totalFlowBytes(buckets[index].InFlow, buckets[index].OutFlow)
	}

	return buckets, nil
}

func loadHourlyTrafficSums(scope packageTrafficScope, start, end time.Time) ([]hourlyTrafficSum, error) {
	query := model.DB.Model(&model.TrafficHourly{})
	if scope.TunnelID != nil {
		query = model.DB.Model(&model.TrafficTunnelHourly{}).
			Where("tunnel_id = ?", *scope.TunnelID)
	}
	query = query.
		Select("bucket_start, COALESCE(SUM(in_flow), 0) AS in_flow, COALESCE(SUM(out_flow), 0) AS out_flow").
		Where("bucket_start >= ? AND bucket_start < ?", start.UnixMilli(), end.UnixMilli())
	if !scope.AllUsers {
		query = query.Where("user_id = ?", scope.UserID)
	}
	var sums []hourlyTrafficSum
	err := query.Group("bucket_start").Order("bucket_start ASC").Scan(&sums).Error
	return sums, err
}

func getTrafficTotalsForWindow(scope packageTrafficScope, start, end time.Time) (packageTrafficTotals, error) {
	sums, err := loadHourlyTrafficSums(scope, start, end)
	if err != nil {
		return packageTrafficTotals{}, err
	}
	var inFlow, outFlow int64
	for _, sum := range sums {
		inFlow = totalFlowBytes(inFlow, sum.InFlow)
		outFlow = totalFlowBytes(outFlow, sum.OutFlow)
	}
	return newPackageTrafficTotals(inFlow, outFlow), nil
}

func trafficTotalsFromStatistics(rows []model.StatisticsFlow) packageTrafficTotals {
	var inFlow, outFlow int64
	for _, row := range rows {
		inFlow = totalFlowBytes(inFlow, row.InFlow)
		outFlow = totalFlowBytes(outFlow, row.OutFlow)
	}
	return newPackageTrafficTotals(inFlow, outFlow)
}

func statisticsFlowFromSum(scope packageTrafficScope, label string, createdTime, inFlow, outFlow int64) model.StatisticsFlow {
	return model.StatisticsFlow{
		UserID:      scope.UserID,
		Flow:        totalFlowBytes(inFlow, outFlow),
		InFlow:      inFlow,
		OutFlow:     outFlow,
		Time:        label,
		CreatedTime: createdTime,
	}
}

func startOfLocalHour(value time.Time) time.Time {
	value = value.In(time.Local)
	// 基于实际时刻截断分钟、秒和纳秒，保留夏令时回拨时重复小时的不同偏移。
	return value.Add(-time.Duration(value.Minute())*time.Minute -
		time.Duration(value.Second())*time.Second -
		time.Duration(value.Nanosecond()))
}

func startOfLocalDay(value time.Time) time.Time {
	value = value.In(time.Local)
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.Local)
}

func calendarDayOffset(start, day time.Time) int {
	if day.Before(start) {
		return -1
	}
	offset := 0
	for cursor := start; cursor.Before(day); cursor = cursor.AddDate(0, 0, 1) {
		offset++
	}
	return offset
}
