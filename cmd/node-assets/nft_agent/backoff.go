package main

import (
	"math/rand"
	"time"
)

// Backoff 生成重连等待时间：1s 起，指数翻倍，±20% 抖动，30s 封顶。
// 与前端 reconnectDelay 使用同一策略，连接成功后必须 Reset。
type Backoff struct {
	attempt int
	random  func() float64
}

// NewBackoff 创建退避策略；random 为 [0,1) 随机源，nil 使用 math/rand。
func NewBackoff(random func() float64) *Backoff {
	if random == nil {
		random = rand.Float64
	}
	return &Backoff{random: random}
}

// Next 返回下一次等待时间并推进尝试计数。
func (b *Backoff) Next() time.Duration {
	base := time.Second << b.attempt
	if base > 30*time.Second || base <= 0 {
		base = 30 * time.Second
	}
	if b.attempt < 63 {
		b.attempt++
	}
	jittered := time.Duration(float64(base) * (0.8 + 0.4*b.random()))
	return jittered
}

// Reset 在连接成功后调用，使下一次失败从 1 秒重新开始。
func (b *Backoff) Reset() {
	b.attempt = 0
}
