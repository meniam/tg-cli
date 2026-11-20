package vectors

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Benchmarks use synthetic data. Fixtures are built once per size and shared
// by every benchmark; TestMain removes them.
var (
	benchMu   sync.Mutex
	benchDirs = map[int]string{}
	benchRoot string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if benchRoot != "" {
		os.RemoveAll(benchRoot)
	}
	os.Exit(code)
}

const benchDim = 1024

// benchFile returns the path of a vectors.db holding n random vectors.
func benchFile(b *testing.B, n int) string {
	b.Helper()
	benchMu.Lock()
	defer benchMu.Unlock()
	if path, ok := benchDirs[n]; ok {
		return path
	}
	if benchRoot == "" {
		dir, err := os.MkdirTemp("", "vectors-bench-")
		if err != nil {
			b.Fatal(err)
		}
		benchRoot = dir
	}
	path := filepath.Join(benchRoot, fmt.Sprintf("vectors-%d.db", n))
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewSource(int64(n)))
	const batch = 500
	for done := 0; done < n; {
		var frags []Fragment
		for i := 0; i < batch && done < n; i, done = i+1, done+1 {
			frags = append(frags, frag(int64(done%500), int64(done/500)*10+1, randVec(rng, benchDim)))
		}
		if err := s.Put(context.Background(), frags); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
	benchDirs[n] = path
	return path
}

func benchSizes(b *testing.B) []int {
	if testing.Short() {
		return []int{10_000}
	}
	return []int{10_000, 100_000}
}

// BenchmarkSearch measures one top-10 query over loaded vectors.
func BenchmarkSearch(b *testing.B) {
	for _, n := range benchSizes(b) {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			s, err := Open(benchFile(b, n))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			query := randVec(rand.New(rand.NewSource(99)), benchDim)
			ctx := context.Background()
			if _, err := s.Search(ctx, query, 10, searchOpts()); err != nil { // loads the vectors
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Search(ctx, query, 10, searchOpts()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSearchFiltered measures the same query when the filter keeps one
// chat in ten.
func BenchmarkSearchFiltered(b *testing.B) {
	for _, n := range benchSizes(b) {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			s, err := Open(benchFile(b, n))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			query := randVec(rand.New(rand.NewSource(99)), benchDim)
			opts := searchOpts()
			opts.Allow = func(chat int64) bool { return chat%10 == 0 }
			ctx := context.Background()
			if _, err := s.Search(ctx, query, 10, opts); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Search(ctx, query, 10, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLoad measures reading every vector from the file into memory and
// reports the heap the loaded vectors hold.
func BenchmarkLoad(b *testing.B) {
	for _, n := range benchSizes(b) {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			s, err := Open(benchFile(b, n))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			ctx := context.Background()
			key := modelKey{testModel, testDigest}
			var heap uint64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s.invalidate()
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				b.StartTimer()
				g, err := s.loadGroup(ctx, key)
				if err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				runtime.ReadMemStats(&after)
				heap = after.HeapAlloc - before.HeapAlloc
				if len(g.metas) != n {
					b.Fatalf("loaded %d rows, want %d", len(g.metas), n)
				}
				b.StartTimer()
			}
			b.ReportMetric(float64(heap)/(1<<20), "heap-MiB")
		})
	}
}

// BenchmarkPut measures storing a batch of 100 vectors.
func BenchmarkPut(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "vectors.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	s.now = func() time.Time { return time.Unix(1, 0) }
	rng := rand.New(rand.NewSource(7))
	frags := make([]Fragment, 100)
	for i := range frags {
		frags[i] = frag(1, int64(i+1), randVec(rng, benchDim))
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Put(ctx, frags); err != nil {
			b.Fatal(err)
		}
	}
}
