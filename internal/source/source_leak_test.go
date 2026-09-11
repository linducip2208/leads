package source

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestSourceCancellationDoesNotLeakGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := NewMockSource(10000).Search(ctx, SearchQuery{Limit: 10000})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		for range ch {
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base+12 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base+12 {
		t.Fatalf("goroutine count grew from %d to %d after cancellation", base, got)
	}
}
