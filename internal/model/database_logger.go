package model

import (
	"io"
	"log"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

func newDatabaseLogger(out io.Writer, level gormlogger.LogLevel) gormlogger.Interface {
	return gormlogger.New(log.New(out, "\r\n", log.LstdFlags), gormlogger.Config{
		SlowThreshold: 200 * time.Millisecond,
		LogLevel:      level,
		// First/Take "record not found" is routine optional-lookup control flow
		// (permissions, sync state, name uniqueness probes). Logging it as an
		// error floods CI and production logs without aiding diagnosis.
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  true,
	})
}
