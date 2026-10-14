package manager

import (
	"math/rand/v2"
	"time"
)

func Backoff(attempt, max int) time.Duration {
	if attempt < 1 {
		return 0
	}
	if attempt > max {
		return 0
	}
	d := time.Second << min(attempt-1, 6)
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}
