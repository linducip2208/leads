package redisx

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDistributedSemaphoreGlobalCapAndRecovery(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer rdb.Close()
	key := "leadforge:test:semaphore:" + time.Now().Format("20060102150405.000000000")
	defer rdb.Del(context.Background(), key)
	a := NewSemaphore(rdb, key, 2, 5*time.Second)
	b := NewSemaphore(rdb, key, 2, 5*time.Second)
	if got := a.Acquire(ctx, "a"); got < 0 {
		t.Fatal("first slot unavailable")
	}
	slotA := a.Acquire(ctx, "b")
	if slotA < 0 {
		t.Fatal("second slot unavailable")
	}
	if got := b.Acquire(ctx, "c"); got >= 0 {
		t.Fatalf("third slot acquired: %d", got)
	}
	a.Release(ctx, slotA, "b")
	if got := b.Acquire(ctx, "c"); got < 0 {
		t.Fatal("slot was not released")
	} else {
		b.Release(ctx, got, "c")
	}
	a.Release(ctx, 0, "a")
}
