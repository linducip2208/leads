package queue

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestCountRecentBeatsIgnoresStaleKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	prefix := "leadforge:test:worker:" + uuid.NewString() + ":"
	recentKey := prefix + "recent"
	staleKey := prefix + "stale"
	defer rdb.Del(context.Background(), recentKey, staleKey)
	if err := Beat(ctx, "127.0.0.1:6379", recentKey, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, staleKey, time.Now().Add(-time.Hour).Format(time.RFC3339), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	n, err := CountRecentBeats(ctx, "127.0.0.1:6379", prefix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("recent heartbeat count = %d, want 1", n)
	}
}
