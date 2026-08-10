package service

import (
	"database/sql"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

type SystemStatus struct {
	StartedAt     int64 `json:"startedAt"`
	UptimeSeconds int64 `json:"uptimeSeconds"`
}

func minTableTime(table, column string) int64 {
	var value sql.NullInt64
	model.DB.Table(table).Select("MIN(" + column + ")").Scan(&value)
	if value.Valid && value.Int64 > 0 {
		return value.Int64
	}
	return 0
}

func earliestDatabaseRecordTime() int64 {
	candidates := []int64{
		minTableTime("user", "created_time"),
		minTableTime("node", "created_time"),
		minTableTime("tunnel", "created_time"),
		minTableTime("forward", "created_time"),
		minTableTime("speed_limit", "created_time"),
		minTableTime("statistics_flow", "created_time"),
		minTableTime("vite_config", "time"),
		minTableTime("proxy_node", "created_time"),
		minTableTime("subscription_profile", "created_time"),
	}
	var earliest int64
	for _, value := range candidates {
		if value <= 0 {
			continue
		}
		if earliest == 0 || value < earliest {
			earliest = value
		}
	}
	if earliest == 0 {
		earliest = time.Now().UnixMilli()
	}
	return earliest
}

func GetSystemStatus() result.R {
	startedAt := earliestDatabaseRecordTime()
	uptime := (time.Now().UnixMilli() - startedAt) / 1000
	if uptime < 0 {
		uptime = 0
	}
	return result.Ok(SystemStatus{
		StartedAt:     startedAt,
		UptimeSeconds: uptime,
	})
}
