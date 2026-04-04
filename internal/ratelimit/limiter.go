package ratelimit

import (
	"sync"

	"golang.org/x/time/rate"
)

type RateManager struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

func NewRateManager(requestsPerSecond float64, maxBurst int) (manager *RateManager) {
	return &RateManager{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Limit(requestsPerSecond),
		burst:    maxBurst,
	}
}

// getLimiter获取一个针对特定用户的限流器
func (rm *RateManager) getLimiter(userID string) (limiter *rate.Limiter) {
	rm.mu.RLock()
	limiter, exists := rm.limiters[userID]
	rm.mu.RUnlock()

	if exists {
		return limiter
	}

	// Double-Check Locking 并发防御机制
	rm.mu.Lock()
	defer rm.mu.Unlock()

	limiter, exists = rm.limiters[userID]
	if !exists {
		// 创建一个新的限流桶，按 rm.rate 的速率滴入令牌，最多攒 rm.burst 个
		limiter = rate.NewLimiter(rm.rate, rm.burst)
		rm.limiters[userID] = limiter
	}
	return limiter
}

func (rm *RateManager) AllowRequest(userID string) (isAllowed bool) {
	limiter := rm.getLimiter(userID)
	// 尝试拿取一个令牌，拿到返回 true，拿不到说明刷屏了，返回 false
	return limiter.Allow()
}
