package model

import (
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const trafficHourMilliseconds = int64(time.Hour / time.Millisecond)

const trafficHourlyBackfillMigration = "traffic-hourly-backfill-v1"

// TrafficHourlyBucketStart 返回上报时间所在的服务器本地整点（毫秒时间戳）。
// 从真实本地时刻截断，兼容非整小时 UTC 偏移和夏令时回拨的重复小时。
func TrafficHourlyBucketStart(at time.Time) int64 {
	local := at.In(time.Local)
	// 从实际时刻回退到该小时起点，保留夏令时回拨时两个同名小时各自的
	// UTC 偏移；用 time.Date 重建墙上时间会把它们错误合并到同一桶。
	return local.Add(-time.Duration(local.Minute())*time.Minute -
		time.Duration(local.Second())*time.Second -
		time.Duration(local.Nanosecond())).UnixMilli()
}

func legacyTrafficHourlyBucketStart(createdTime int64) int64 {
	return TrafficHourlyBucketStart(time.UnixMilli(createdTime)) - trafficHourMilliseconds
}

// backfillTrafficHourly 将旧小时快照幂等迁移到实时账本。
// StatisticsFlow 的 CreatedTime 是任务执行时间，实际增量属于它之前一小时；若同一
// 用户小时已有实时账本则以实时值为准，不叠加旧快照。旧库无法还原上下行方向时，
// 将总增量保守归入下行，至少保证总流量和历史趋势不丢失。
func backfillTrafficHourly(db *gorm.DB) error {
	var completed int64
	if err := db.Model(&DataMigration{}).
		Where("name = ?", trafficHourlyBackfillMigration).
		Count(&completed).Error; err != nil {
		return fmt.Errorf("读取小时流量迁移状态失败: %w", err)
	}
	if completed > 0 {
		return nil
	}
	if err := backfillTrafficHourlyAt(db, time.Now()); err != nil {
		return err
	}
	marker := DataMigration{Name: trafficHourlyBackfillMigration, CompletedTime: time.Now().UnixMilli()}
	if err := db.Create(&marker).Error; err != nil {
		return fmt.Errorf("记录小时流量迁移状态失败: %w", err)
	}
	return nil
}

func backfillTrafficHourlyAt(db *gorm.DB, now time.Time) error {
	var legacy []StatisticsFlow
	if err := db.Order("user_id ASC, created_time ASC, id ASC").Find(&legacy).Error; err != nil {
		return fmt.Errorf("读取旧小时流量统计失败: %w", err)
	}

	type bucketKey struct {
		userID      int64
		bucketStart int64
	}
	aggregated := make(map[bucketKey]TrafficHourly, len(legacy))
	// 每个用户通常有几十条小时快照；不能按快照总数预分配“每用户最新值”
	// map，否则大库首次迁移会无谓占用数倍内存。
	latestByUser := make(map[int64]StatisticsFlow)
	for _, snapshot := range legacy {
		latestByUser[snapshot.UserID] = snapshot
		key := bucketKey{
			userID:      snapshot.UserID,
			bucketStart: legacyTrafficHourlyBucketStart(snapshot.CreatedTime),
		}
		inFlow, outFlow := snapshot.InFlow, snapshot.OutFlow
		if inFlow == 0 && outFlow == 0 {
			inFlow = snapshot.Flow
		}
		row, exists := aggregated[key]
		if !exists {
			row = TrafficHourly{
				UserID:      key.userID,
				BucketStart: key.bucketStart,
				CreatedTime: snapshot.CreatedTime,
				UpdatedTime: snapshot.CreatedTime,
			}
		}
		row.InFlow = safeTrafficTotal(row.InFlow, inFlow)
		row.OutFlow = safeTrafficTotal(row.OutFlow, outFlow)
		if snapshot.CreatedTime < row.CreatedTime {
			row.CreatedTime = snapshot.CreatedTime
		}
		if snapshot.CreatedTime > row.UpdatedTime {
			row.UpdatedTime = snapshot.CreatedTime
		}
		aggregated[key] = row
	}

	// 升级可能发生在两个旧整点快照之间。此时最新快照之后、面板重启之前已
	// 计入用户累计值的流量还没有历史行；若基线只相隔当前/上一小时，可精确
	// 补入基线所在小时。已有实时小时行会在最终 upsert 时保持权威、不被覆盖。
	var users []User
	if err := db.Select("id", "in_flow", "out_flow", "created_time").Find(&users).Error; err != nil {
		return fmt.Errorf("读取旧用户累计流量失败: %w", err)
	}
	currentBucket := TrafficHourlyBucketStart(now)
	for _, user := range users {
		snapshot, ok := latestByUser[user.ID]
		if !ok {
			// 用户若在当前小时才创建，尚未来得及生成旧整点快照；其现有累计值
			// 全部属于当前小时，可以无歧义补入。更早创建但无快照的账号无法可靠
			// 判断流量日期，不能猜测分桶。
			if TrafficHourlyBucketStart(time.UnixMilli(user.CreatedTime)) != currentBucket {
				continue
			}
			inFlow := nonNegativeTrafficDelta(user.InFlow, 0)
			outFlow := nonNegativeTrafficDelta(user.OutFlow, 0)
			if inFlow == 0 && outFlow == 0 {
				continue
			}
			key := bucketKey{userID: user.ID, bucketStart: currentBucket}
			aggregated[key] = TrafficHourly{
				UserID: user.ID, BucketStart: currentBucket, InFlow: inFlow, OutFlow: outFlow,
				CreatedTime: now.UnixMilli(), UpdatedTime: now.UnixMilli(),
			}
			continue
		}
		baselineBucket := TrafficHourlyBucketStart(time.UnixMilli(snapshot.CreatedTime))
		bucketAge := currentBucket - baselineBucket
		if bucketAge != 0 && bucketAge != trafficHourMilliseconds {
			continue
		}

		var inFlow, outFlow int64
		hasDirectionalBaseline := snapshot.TotalInFlow > 0 || snapshot.TotalOutFlow > 0 || snapshot.TotalFlow == 0
		if hasDirectionalBaseline {
			inFlow = nonNegativeTrafficDelta(user.InFlow, snapshot.TotalInFlow)
			outFlow = nonNegativeTrafficDelta(user.OutFlow, snapshot.TotalOutFlow)
		} else {
			inFlow = nonNegativeTrafficDelta(safeTrafficTotal(user.InFlow, user.OutFlow), snapshot.TotalFlow)
		}
		if inFlow == 0 && outFlow == 0 {
			continue
		}

		key := bucketKey{userID: user.ID, bucketStart: baselineBucket}
		row, exists := aggregated[key]
		if !exists {
			row = TrafficHourly{
				UserID: user.ID, BucketStart: baselineBucket,
				CreatedTime: now.UnixMilli(), UpdatedTime: now.UnixMilli(),
			}
		} else {
			row.UpdatedTime = now.UnixMilli()
		}
		row.InFlow = safeTrafficTotal(row.InFlow, inFlow)
		row.OutFlow = safeTrafficTotal(row.OutFlow, outFlow)
		aggregated[key] = row
	}

	rows := make([]TrafficHourly, 0, len(aggregated))
	for _, row := range aggregated {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UserID == rows[j].UserID {
			return rows[i].BucketStart < rows[j].BucketStart
		}
		return rows[i].UserID < rows[j].UserID
	})
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "bucket_start"}},
		DoNothing: true,
	}).CreateInBatches(&rows, 500).Error; err != nil {
		return fmt.Errorf("回填旧小时流量统计失败: %w", err)
	}
	return nil
}

func nonNegativeTrafficDelta(current, baseline int64) int64 {
	if current < 0 {
		current = 0
	}
	if baseline < 0 {
		baseline = 0
	}
	if current < baseline {
		// 套餐清零后累计值会回退；此时清零后的当前值就是尚未入旧快照的增量。
		return current
	}
	return current - baseline
}

func safeTrafficTotal(left, right int64) int64 {
	if left < 0 {
		left = 0
	}
	if right < 0 {
		right = 0
	}
	if left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
