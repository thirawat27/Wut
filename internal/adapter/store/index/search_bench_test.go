package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

// benchReader builds an index the size of the real tldr corpus.
func benchReader(tb testing.TB) *Reader {
	tb.Helper()
	b := NewBuilder("bench")
	for i := 0; i < 4000; i++ {
		p := knowledge.Page{
			Name:        fmt.Sprintf("cmd%04d", i),
			Platform:    knowledge.PlatformCommon,
			Description: fmt.Sprintf("compress archive extract files folder tool number %d for common tasks", i),
		}
		for e := 0; e < 6; e++ {
			p.Examples = append(p.Examples, knowledge.Example{
				Description: fmt.Sprintf("compress a folder to an archive variant %d", e),
				Command:     fmt.Sprintf("cmd%04d -c%d {{folder}}", i, e),
			})
		}
		b.Add(p)
	}
	path := filepath.Join(tb.TempDir(), "bench.idx")
	if err := b.WriteTo(path); err != nil {
		tb.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	return r
}

// A question whose every word is common matches most of the corpus. Scoring
// used to decode a page per candidate unit, so this cost half a second and a
// gigabyte of allocation for one question.
func BenchmarkSearchBroad(b *testing.B) {
	r := benchReader(b)
	q := knowledge.ParseQuery("compress a folder to an archive")
	q.NoSemantic = true
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Search(ctx, q, 8); err != nil {
			b.Fatal(err)
		}
	}
}
