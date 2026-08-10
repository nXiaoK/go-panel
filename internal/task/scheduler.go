package task

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
)

// Start 启动定时任务（对应 ResetFlowAsync / StatisticsFlowAsync）
func Start() *cron.Cron {
	c := cron.New(cron.WithSeconds())

	// 每天 00:00:05 流量重置 + 到期处理（与 Java cron "5 0 0 * * ?" 一致）
	c.AddFunc("5 0 0 * * *", recoverJob("流量重置", ResetFlowJob))

	// 每小时整点流量统计（与 Java cron "0 0 * * * ?" 一致）
	c.AddFunc("0 0 * * * *", recoverJob("流量统计", StatisticsFlowJob))

	// 每 30 秒重试待同步/同步失败的节点，收敛期望状态。
	c.AddFunc("*/30 * * * * *", recoverJob("节点期望状态对账", ReconcileNodesJob))

	// 每分钟检查一次 Cloudflare R2 计划时间；服务层通过持久成功日期去重，
	// 错过计划时间（重启/维护）会补跑，失败则按 15 分钟退避重试。
	c.AddFunc("15 * * * * *", recoverJob("Cloudflare R2 自动备份", R2BackupJob))

	// 每 30 分钟查看一次缓存状态；只有达到 UPDATE_CHECK_INTERVAL 时才访问 GitHub，
	// 外部网络失败不会占用数据库维护门控，也不会影响其他定时任务。
	c.AddFunc("0 */30 * * * *", recoverExternalJob("GitHub 稳定版更新检查", UpdateCheckJob))

	c.Start()
	log.Printf("定时任务已启动：流量重置(每日00:00:05)、流量统计(每小时)、节点对账(每30秒)、R2备份检查(每分钟)、版本检查(每30分钟读取缓存)")

	activeCronMu.Lock()
	activeCron = c
	activeCronMu.Unlock()
	return c
}

var (
	activeCronMu sync.Mutex
	activeCron   *cron.Cron
)

// Stop 停止调度器派发并等待正在运行的任务结束。幂等。
func Stop() {
	activeCronMu.Lock()
	c := activeCron
	activeCron = nil
	activeCronMu.Unlock()
	if c == nil {
		return
	}
	<-c.Stop().Done()
}

func recoverJob(name string, job func()) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("定时任务 %s panic 已恢复: %v\n%s", name, r, debug.Stack())
			}
		}()
		// 数据库恢复维护期间跳过本轮，下个周期自然重试。
		leave, ok := model.Gate.Enter()
		if !ok {
			log.Printf("定时任务 %s 跳过：数据库维护中", name)
			return
		}
		defer leave()
		job()
	}
}

func recoverExternalJob(name string, job func()) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("定时任务 %s panic 已恢复: %v\n%s", name, r, debug.Stack())
			}
		}()
		job()
	}
}

// ResetFlowJob 每日流量重置 + 过期账号/隧道处理
func ResetFlowJob() {
	log.Printf("开始执行流量重置任务")
	now := time.Now()
	currentDay := now.Day()
	lastDayOfMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()

	resetUserFlow(currentDay, lastDayOfMonth)
	resetUserTunnelFlow(currentDay, lastDayOfMonth)
	expireUsers()
	expireUserTunnels()
	log.Printf("流量重置任务执行完成")
}

// resetUserFlow 重置当日匹配的用户流量（含月末边界）
func resetUserFlow(currentDay, lastDayOfMonth int) {
	q := model.DB.Model(&model.User{}).Where("flow_reset_time <> 0")
	if currentDay == lastDayOfMonth {
		q = q.Where("flow_reset_time = ? OR flow_reset_time > ?", currentDay, lastDayOfMonth)
	} else {
		q = q.Where("flow_reset_time = ?", currentDay)
	}
	res := q.Updates(map[string]interface{}{"in_flow": 0, "out_flow": 0})
	if res.RowsAffected > 0 {
		log.Printf("重置了 %d 个用户的流量", res.RowsAffected)
	}
}

// resetUserTunnelFlow 重置当日匹配的用户隧道流量
func resetUserTunnelFlow(currentDay, lastDayOfMonth int) {
	q := model.DB.Model(&model.UserTunnel{}).Where("flow_reset_time <> 0")
	if currentDay == lastDayOfMonth {
		q = q.Where("flow_reset_time = ? OR flow_reset_time > ?", currentDay, lastDayOfMonth)
	} else {
		q = q.Where("flow_reset_time = ?", currentDay)
	}
	res := q.Updates(map[string]interface{}{"in_flow": 0, "out_flow": 0})
	if res.RowsAffected > 0 {
		log.Printf("重置了 %d 个用户隧道的流量", res.RowsAffected)
	}
}

// expireUsers 处理过期账号：暂停其全部转发并禁用账号
func expireUsers() {
	nowMs := time.Now().UnixMilli()
	var users []model.User
	if err := model.DB.Where("role_id <> 0 AND status = 1 AND exp_time IS NOT NULL AND exp_time < ?", nowMs).Find(&users).Error; err != nil {
		log.Printf("查询过期用户失败: %v", err)
		return
	}
	if len(users) == 0 {
		return
	}
	// 批量加载所有过期用户的活跃转发，避免 N+1 查询
	userIDs := make([]int64, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}
	var allForwards []model.Forward
	if err := model.DB.Where("user_id IN ? AND status = 1", userIDs).Find(&allForwards).Error; err != nil {
		log.Printf("查询过期用户转发失败: %v", err)
		return
	}
	service.PauseForwards(allForwards)
	for _, user := range users {
		if err := model.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("status", 0).Error; err != nil {
			log.Printf("停用过期用户 %s 失败: %v", user.User, err)
			continue
		}
		log.Printf("用户 %s 已到期，停用账号", user.User)
	}
}

// expireUserTunnels 处理过期用户隧道：暂停对应转发并禁用权限
func expireUserTunnels() {
	nowMs := time.Now().UnixMilli()
	var uts []model.UserTunnel
	if err := model.DB.Where("status = 1 AND exp_time IS NOT NULL AND exp_time < ?", nowMs).Find(&uts).Error; err != nil {
		log.Printf("查询过期用户隧道失败: %v", err)
		return
	}
	if len(uts) == 0 {
		return
	}
	// 批量加载所有过期隧道的活跃转发，避免 N+1 查询
	type utKey struct {
		userID, tunnelID int64
	}
	keys := make([]utKey, 0, len(uts))
	tunnelIDs := make([]int64, 0, len(uts))
	userIDs := make([]int64, 0, len(uts))
	for _, ut := range uts {
		keys = append(keys, utKey{ut.UserID, ut.TunnelID})
		tunnelIDs = append(tunnelIDs, ut.TunnelID)
		userIDs = append(userIDs, ut.UserID)
	}
	var allForwards []model.Forward
	if err := model.DB.Where("tunnel_id IN ? AND user_id IN ? AND status = 1", tunnelIDs, userIDs).Find(&allForwards).Error; err != nil {
		log.Printf("查询过期隧道转发失败: %v", err)
		return
	}
	// 仅暂停属于过期 (userID, tunnelID) 组合的转发
	keySet := make(map[utKey]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var matched []model.Forward
	for _, f := range allForwards {
		if keySet[utKey{f.UserID, f.TunnelID}] {
			matched = append(matched, f)
		}
	}
	service.PauseForwards(matched)
	for _, ut := range uts {
		if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", ut.ID).Update("status", 0).Error; err != nil {
			log.Printf("停用过期用户隧道 %d 失败: %v", ut.ID, err)
			continue
		}
		log.Printf("用户隧道权限 %d 已到期，停用", ut.ID)
	}
}

// ReconcileNodesJob 周期性收敛节点期望状态：重试上次未应用/失败的节点。
func ReconcileNodesJob() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	service.ReconcilePendingNodes(ctx)
}

// R2BackupJob 执行一次到期判断并在需要时上传一致性站点快照。
func R2BackupJob() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ran, summary, err := service.RunScheduledR2Backup(ctx, time.Now())
	if err != nil {
		log.Printf("Cloudflare R2 自动备份失败: %v", err)
		return
	}
	if ran && summary != nil {
		log.Printf("Cloudflare R2 自动备份完成：对象=%s，大小=%d，清理=%d", summary.ObjectKey, summary.Size, summary.DeletedObjects)
	}
}

// UpdateCheckJob 刷新 GitHub Release 缓存；关闭检查或缓存仍有效时立即返回。
func UpdateCheckJob() {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if _, err := service.RefreshPanelUpdate(ctx); err != nil {
		log.Printf("GitHub 稳定版更新检查失败: %v", err)
	}
}

// JobStats 汇总一次统计任务的执行情况。
type JobStats struct {
	Users    int
	Batches  int
	Inserted int
}

// statisticsInsert 单批快照落库；测试可替换以注入失败。
var statisticsInsert = func(rows []model.StatisticsFlow) error {
	return model.DB.Create(&rows).Error
}

func swapStatisticsInsert(fn func([]model.StatisticsFlow) error) func() {
	prev := statisticsInsert
	statisticsInsert = fn
	return func() { statisticsInsert = prev }
}

// StatisticsFlowJob 每小时用户流量快照（保留 31 天）
func StatisticsFlowJob() {
	if _, err := statisticsFlowJobAt(time.Now(), 1000); err != nil {
		log.Printf("流量统计任务失败: %v", err)
	}
}

// statisticsFlowJobAt 以 keyset 分页（id > lastID）逐批构建并立即落库快照：
// 每批最多 batchSize 个用户，写完即释放，不再全量累积到内存；任一批
// 加载或写入失败立即返回，不加载后续批次。过期历史仅在全部批次成功后清理。
func statisticsFlowJobAt(now time.Time, batchSize int) (JobStats, error) {
	stats := JobStats{}
	localNow := now.In(now.Location())
	// 从实际时刻截断到整点，保留夏令时回拨时重复小时各自的 UTC 偏移。
	currentHour := localNow.Add(-time.Duration(localNow.Minute())*time.Minute -
		time.Duration(localNow.Second())*time.Second -
		time.Duration(localNow.Nanosecond()))
	// 整点快照记录的是“刚结束的上一小时”增量；使用上一小时标签，避免
	// 00:00 时把昨日 23:00-24:00 的流量错误归入今天。
	hourString := currentHour.Add(-time.Hour).Format("15:00")
	nowMs := now.UnixMilli()

	var lastID int64
	for {
		var users []model.User
		if err := model.DB.Where("id > ?", lastID).Order("id").Limit(batchSize).Find(&users).Error; err != nil {
			return stats, fmt.Errorf("加载用户批次失败(lastID=%d): %w", lastID, err)
		}
		if len(users) == 0 {
			break
		}
		batch := make([]model.StatisticsFlow, 0, len(users))
		for _, user := range users {
			batch = append(batch, buildStatisticsSnapshot(user, hourString, nowMs))
		}
		if err := statisticsInsert(batch); err != nil {
			return stats, fmt.Errorf("写入流量统计快照失败: %w", err)
		}
		stats.Batches++
		stats.Users += len(users)
		stats.Inserted += len(batch)
		lastID = users[len(users)-1].ID
		if len(users) < batchSize {
			break
		}
	}

	// 删除 31 天前的数据，保留 dashboard 30D 统计窗口。
	// 仅在全部快照批次成功后清理，失败时保留旧历史供重试。
	cutoff := nowMs - 31*24*60*60*1000
	if err := model.DB.Where("created_time < ?", cutoff).Delete(&model.StatisticsFlow{}).Error; err != nil {
		log.Printf("清理过期流量统计失败: %v", err)
	}
	if err := model.DB.Where("bucket_start < ?", cutoff).Delete(&model.TrafficHourly{}).Error; err != nil {
		log.Printf("清理过期小时流量账本失败: %v", err)
	}
	if err := model.DB.Where("bucket_start < ?", cutoff).Delete(&model.TrafficTunnelHourly{}).Error; err != nil {
		log.Printf("清理过期隧道小时流量账本失败: %v", err)
	}
	return stats, nil
}

func buildStatisticsSnapshot(user model.User, hourString string, nowMs int64) model.StatisticsFlow {
	currentIn := user.InFlow
	currentOut := user.OutFlow
	currentTotal := currentIn + currentOut

	var last model.StatisticsFlow
	increment := currentTotal
	incrementIn := currentIn
	incrementOut := currentOut
	if err := model.DB.Where("user_id = ?", user.ID).Order("id DESC").First(&last).Error; err == nil {
		increment = currentTotal - last.TotalFlow
		if increment < 0 {
			increment = currentTotal
		}
		hasDirectionalBaseline := last.TotalInFlow > 0 || last.TotalOutFlow > 0 || last.TotalFlow == 0
		if hasDirectionalBaseline {
			incrementIn = currentIn - last.TotalInFlow
			if incrementIn < 0 {
				incrementIn = currentIn
			}
			incrementOut = currentOut - last.TotalOutFlow
			if incrementOut < 0 {
				incrementOut = currentOut
			}
		} else {
			// Legacy rows do not contain directional cumulative counters, so the
			// exact split is unknowable until this snapshot establishes a baseline.
			incrementIn = 0
			incrementOut = 0
		}
	}

	return model.StatisticsFlow{
		UserID:       user.ID,
		Flow:         increment,
		InFlow:       incrementIn,
		OutFlow:      incrementOut,
		TotalFlow:    currentTotal,
		TotalInFlow:  currentIn,
		TotalOutFlow: currentOut,
		Time:         hourString,
		CreatedTime:  nowMs,
	}
}
