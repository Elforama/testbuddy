package match

import (
	"fmt"
	"sync"
	"testing"
)

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

func BenchmarkCompileCached(b *testing.B) {
	compile("Sign in")
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			compile("Sign in")
		}
	})
}
