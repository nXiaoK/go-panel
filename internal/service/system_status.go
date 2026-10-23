package service

import (
	"time"

	"github.com/nXiaoK/go-panel/internal/result"
)

// processStartedAt 在服务包初始化时记录进程启动时间，不受数据库导入、恢复或历史记录影响。
var processStartedAt = time.Now()

type SystemStatus struct {
	StartedAt     int64 `json:"startedAt"`     // 面板进程启动时间（毫秒）。
	UptimeSeconds int64 `json:"uptimeSeconds"` // 面板进程已运行秒数，不包含数据库年龄。
}

func GetSystemStatus() result.R {
	uptime := int64(time.Since(processStartedAt) / time.Second)
	if uptime < 0 {
		uptime = 0
	}
	return result.Ok(SystemStatus{
		StartedAt:     processStartedAt.UnixMilli(),
		UptimeSeconds: uptime,
	})
}
