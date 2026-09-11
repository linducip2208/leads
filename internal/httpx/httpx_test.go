package httpx

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterIsRaceSafe(t *testing.T) {
	rl := NewRateLimiter(100000, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = rl.Allow("key-" + string(rune('a'+n%4)))
			}
		}(i)
	}
	wg.Wait()
}
