package ratelimit

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

var incrementRequest = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

func RateLimiterMiddleware(rdb *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limit < 1 || window.Milliseconds() <= 0 {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "rate limiter is misconfigured"})
			return
		}

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		key := fmt.Sprintf("rate_limit:%s:%s", route, c.ClientIP())
		count, err := incrementRequest.Run(
			c.Request.Context(),
			rdb,
			[]string{key},
			window.Milliseconds(),
		).Int64()
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			return
		}

		if count > int64(limit) {
			retryAfter := int(math.Ceil(window.Seconds()))
			c.Header("Retry-After", fmt.Sprint(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "too many requests",
				"retry_after": retryAfter,
			})
			return
		}

		c.Next()
	}
}
