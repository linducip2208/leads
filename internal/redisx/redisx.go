// Package redisx holds small Redis primitives (distributed semaphore).
package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Semaphore is an N-permit distributed semaphore with self-healing TTL.
// Permits are individual keys; crashed holders expire automatically.
type Semaphore struct {
	rdb     *redis.Client
	key     string
	permits int
	ttl     time.Duration
}

// NewSemaphore builds a semaphore. permits<=0 disables (Acquire always true).
func NewSemaphore(rdb *redis.Client, key string, permits int, ttl time.Duration) *Semaphore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &Semaphore{rdb: rdb, key: key, permits: permits, ttl: ttl}
}

var luaAcquire = redis.NewScript(`
for i = 0, tonumber(ARGV[1]) - 1 do
  local k = KEYS[1] .. ':' .. i
  if redis.call('SET', k, ARGV[2], 'NX', 'PX', ARGV[3]) then
    return i
  end
end
return -1`)

var luaRelease = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// Acquire takes a permit slot or returns -1.
func (s *Semaphore) Acquire(ctx context.Context, token string) int {
	if s == nil || s.rdb == nil || s.permits <= 0 {
		return 0 // disabled: allow (local limiter still applies)
	}
	n, err := luaAcquire.Run(ctx, s.rdb, []string{s.key}, s.permits, token, int64(s.ttl/time.Millisecond)).Int()
	if err != nil {
		return -1
	}
	return n
}

// Release frees a slot held by token.
func (s *Semaphore) Release(ctx context.Context, slot int, token string) {
	if s == nil || s.rdb == nil || slot < 0 {
		return
	}
	_, _ = luaRelease.Run(ctx, s.rdb, []string{s.key + ":" + itoa(slot)}, token).Int()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
