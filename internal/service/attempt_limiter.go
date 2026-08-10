package service

import (
	"strings"
	"sync"
	"time"
)

const (
	attemptWindow    = 15 * time.Minute
	pairAttemptLimit = 5
	ipAttemptLimit   = 50
)

type AttemptLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	pairs       map[string][]time.Time
	ips         map[string][]time.Time
	pairPending map[string]int
	ipPending   map[string]int
}

func NewAttemptLimiter(now func() time.Time) *AttemptLimiter {
	return &AttemptLimiter{
		now:         now,
		pairs:       make(map[string][]time.Time),
		ips:         make(map[string][]time.Time),
		pairPending: make(map[string]int),
		ipPending:   make(map[string]int),
	}
}

func attemptKey(ip, username string) string {
	return ip + "|" + strings.ToLower(strings.TrimSpace(username))
}

func (l *AttemptLimiter) Allow(ip, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(l.now())
	key := attemptKey(ip, username)
	if len(l.pairs[key])+l.pairPending[key] >= pairAttemptLimit || len(l.ips[ip])+l.ipPending[ip] >= ipAttemptLimit {
		return false
	}
	l.pairPending[key]++
	l.ipPending[ip]++
	return true
}

func (l *AttemptLimiter) Failure(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)
	key := attemptKey(ip, username)
	if !l.releasePendingLocked(ip, key) {
		return
	}
	l.pairs[key] = append(l.pairs[key], now)
	l.ips[ip] = append(l.ips[ip], now)
}

func (l *AttemptLimiter) Success(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(l.now())
	key := attemptKey(ip, username)
	if !l.releasePendingLocked(ip, key) {
		return
	}
	delete(l.pairs, key)
}

func (l *AttemptLimiter) Cancel(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(l.now())
	l.releasePendingLocked(ip, attemptKey(ip, username))
}

func (l *AttemptLimiter) releasePendingLocked(ip, key string) bool {
	if l.pairPending[key] <= 0 || l.ipPending[ip] <= 0 {
		return false
	}
	if l.pairPending[key] == 1 {
		delete(l.pairPending, key)
	} else {
		l.pairPending[key]--
	}
	if l.ipPending[ip] == 1 {
		delete(l.ipPending, ip)
	} else {
		l.ipPending[ip]--
	}
	return true
}

func (l *AttemptLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-attemptWindow)
	prune := func(attempts map[string][]time.Time) {
		for key, timestamps := range attempts {
			firstCurrent := 0
			for firstCurrent < len(timestamps) && timestamps[firstCurrent].Before(cutoff) {
				firstCurrent++
			}
			if firstCurrent == len(timestamps) {
				delete(attempts, key)
				continue
			}
			if firstCurrent > 0 {
				attempts[key] = append([]time.Time(nil), timestamps[firstCurrent:]...)
			}
		}
	}

	prune(l.pairs)
	prune(l.ips)
}
