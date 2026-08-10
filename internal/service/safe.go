package service

import (
	"log"
	"runtime/debug"
)

// SafeGo 启动带 panic 恢复的后台 goroutine，避免单个任务 panic 拖垮整个进程。
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("服务后台任务 %s panic 已恢复: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
