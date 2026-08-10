package ws

import (
	"log"
	"runtime/debug"
)

func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("WebSocket 后台任务 %s panic 已恢复: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
