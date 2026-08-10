package service

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

type AuthenticatedNode struct {
	ID          int64
	ForwardMode string
}

var (
	ErrInvalidNodeSecret = errors.New("invalid node secret")
	ErrFlowNodeMismatch  = errors.New("flow node mismatch")
	ErrInvalidFlowReport = errors.New("invalid flow report")
	ErrFlowSequence      = errors.New("invalid flow sequence")
	ErrFlowBatchConflict = errors.New("flow batch conflict")
)

// constSecretCacheTTL bounds how long rotated or deleted node credentials can
// remain usable without another database lookup.
var constSecretCacheTTL = 5 * time.Minute

type secretCacheEntry struct {
	node     AuthenticatedNode
	expireAt time.Time
}

var (
	secretCacheMu         sync.RWMutex
	secretCacheItems      = make(map[string]secretCacheEntry)
	secretCacheGeneration uint64
)

func secretCacheLoad(secret string) (AuthenticatedNode, bool) {
	secretCacheMu.RLock()
	entry, ok := secretCacheItems[secret]
	secretCacheMu.RUnlock()
	if !ok {
		return AuthenticatedNode{}, false
	}
	if time.Now().After(entry.expireAt) {
		secretCacheDelete(secret)
		return AuthenticatedNode{}, false
	}
	return entry.node, true
}

func secretCacheGenerationSnapshot() uint64 {
	secretCacheMu.RLock()
	generation := secretCacheGeneration
	secretCacheMu.RUnlock()
	return generation
}

func secretCacheGenerationMatches(generation uint64) bool {
	secretCacheMu.RLock()
	matches := secretCacheGeneration == generation
	secretCacheMu.RUnlock()
	return matches
}

func secretCacheStoreIfGeneration(secret string, node AuthenticatedNode, generation uint64) bool {
	secretCacheMu.Lock()
	defer secretCacheMu.Unlock()
	if secretCacheGeneration != generation {
		return false
	}
	secretCacheItems[secret] = secretCacheEntry{
		node:     node,
		expireAt: time.Now().Add(constSecretCacheTTL),
	}
	return true
}

func secretCacheDelete(secret string) {
	secretCacheMu.Lock()
	delete(secretCacheItems, secret)
	secretCacheMu.Unlock()
}

func invalidateAllSecretCache() {
	secretCacheMu.Lock()
	secretCacheItems = make(map[string]secretCacheEntry)
	secretCacheGeneration++
	secretCacheMu.Unlock()
}

// AuthenticateNodeSecret returns the immutable node context used to authorize
// one report. Authentication failures deliberately share one public error.
func AuthenticateNodeSecret(secret string) (AuthenticatedNode, error) {
	if secret == "" {
		return AuthenticatedNode{}, ErrInvalidNodeSecret
	}
	if node, ok := secretCacheLoad(secret); ok {
		return node, nil
	}

	for {
		generation := secretCacheGenerationSnapshot()
		var record model.Node
		if err := model.DB.Select("id", "forward_mode").Where("secret = ?", secret).First(&record).Error; err != nil {
			if !secretCacheGenerationMatches(generation) {
				continue
			}
			return AuthenticatedNode{}, ErrInvalidNodeSecret
		}
		node := AuthenticatedNode{ID: record.ID, ForwardMode: record.ForwardMode}
		if secretCacheStoreIfGeneration(secret, node, generation) {
			return node, nil
		}
	}
}

// InvalidateSecretCache clears cached node context after a secret or node update.
func InvalidateSecretCache(secret string) {
	if secret == "" {
		return
	}
	secretCacheMu.Lock()
	delete(secretCacheItems, secret)
	secretCacheGeneration++
	secretCacheMu.Unlock()
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	log.Printf("已清除节点密钥缓存: %s", prefix)
}
