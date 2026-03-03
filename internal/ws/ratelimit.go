package ws

import "golang.org/x/time/rate"

const (
	wsRateLimit         = rate.Limit(30.0 / 60.0) // 30 msg/min = 0.5/sec
	wsRateBurst         = 5                        // allow short bursts of 5
	wsMaxConsecutiveHit = 10                        // disconnect after 10 consecutive breaches
)

func newClientLimiter() *rate.Limiter {
	return rate.NewLimiter(wsRateLimit, wsRateBurst)
}
