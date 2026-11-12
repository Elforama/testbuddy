package match

import (
	"fmt"
	"sync"
	"testing"
)

// issue #65: the regex cache kept one compiled pattern per distinct pattern for the life of
// the process, so a long session, or a flow whose interpolated selectors make a new pattern
// each iteration, grew it without bound. It holds at most reCacheSize patterns.
func TestCompileCacheIsBounded(t *testing.T) {
	for i := range 3 * reCacheSize {
		p := fmt.Sprintf("bounded-%d", i)
		if !TextMatches(p, p) {
			t.Fatalf("TextMatches(%q, %q) = false", p, p)
		}
	}
	if n := cacheLen(); n > reCacheSize {
		t.Fatalf("after %d distinct patterns the cache holds %d, want at most %d", 3*reCacheSize, n, reCacheSize)
	}
}

// issue #65: eviction drops the least recently used pattern, so the handful a flow keeps
// using stay compiled while new ones stream past.
func TestCompileCacheKeepsRecentlyUsed(t *testing.T) {
	hot := compile("hot-pattern.*")
	for i := range 3 * reCacheSize {
		compile(fmt.Sprintf("streaming-%d", i))
		if i%(reCacheSize/4) == 0 {
			if got := compile("hot-pattern.*"); got != hot {
				t.Fatalf("after %d new patterns the pattern in use was compiled again", i)
			}
		}
	}
	if got := compile("hot-pattern.*"); got != hot {
		t.Fatal("the pattern in use was evicted")
	}
}

// issue #65: the devices of a multi-device flow match from several goroutines at once; the
// bounded cache stays consistent and bounded under that (run with -race).
func TestCompileCacheConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range reCacheSize {
				shared := fmt.Sprintf("shared-%d", i%16)
				own := fmt.Sprintf("own-%d-%d", g, i)
				if !TextMatches(shared, shared) || !TextMatches(own, own) || TextMatches(own, shared) {
					t.Errorf("wrong match for %q / %q", shared, own)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := cacheLen(); n > reCacheSize {
		t.Fatalf("cache holds %d, want at most %d", n, reCacheSize)
	}
}

// The common case: a flow reusing a pattern it already compiled.
func BenchmarkCompileCached(b *testing.B) {
	compile("Sign in")
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			compile("Sign in")
		}
	})
}
