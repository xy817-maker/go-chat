package https_server

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"kama_chat_server/internal/config"
	"kama_chat_server/pkg/zlog"
	"net/http"
	"sync"
	"time"
)

// ipLimiter 单个 IP 的令牌桶及其最近访问时间（用于惰性清理）
type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	ipLimiters  sync.Map // ip -> *ipLimiter
	cleanupOnce sync.Once
)

// getLimiter 获取或创建某 IP 的令牌桶（令牌桶算法：以 rps 速率补充令牌，桶容量 burst）
func getLimiter(ip string, rps float64, burst int) *rate.Limiter {
	if val, ok := ipLimiters.Load(ip); ok {
		if l, ok := val.(*ipLimiter); ok {
			l.lastSeen = time.Now()
			return l.limiter
		}
	}
	l := &ipLimiter{
		limiter:  rate.NewLimiter(rate.Limit(rps), burst),
		lastSeen: time.Now(),
	}
	val, _ := ipLimiters.LoadOrStore(ip, l)
	if ll, ok := val.(*ipLimiter); ok {
		return ll.limiter
	}
	return l.limiter
}

// startCleanup 启动后台 goroutine，定期移除长时间未访问的 IP 条目，防止 map 无限增长
func startCleanup() {
	cleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute * 5)
			defer ticker.Stop()
			for range ticker.C {
				ipLimiters.Range(func(key, val interface{}) bool {
					if l, ok := val.(*ipLimiter); ok {
						if time.Since(l.lastSeen) > time.Minute*10 {
							ipLimiters.Delete(key)
						}
					}
					return true
				})
			}
		}()
	})
}

// RateLimitMiddleware 基于 IP 的令牌桶下载限流中间件。
// 从配置读取 rps/burst，对每个客户端 IP 维护独立令牌桶，
// 令牌不足时直接返回 429，不进入静态文件服务。
func RateLimitMiddleware() gin.HandlerFunc {
	rlConfig := config.GetConfig().RateLimitConfig
	startCleanup()
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := getLimiter(ip, rlConfig.Rps, rlConfig.Burst)
		if !limiter.Allow() {
			zlog.Info("下载限流命中，ip=" + ip)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}
