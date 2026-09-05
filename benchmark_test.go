package gobridge

import (
	"context"
	"testing"
	"time"
)

func BenchmarkCall(b *testing.B) {
	r := New()
	_ = Register(r, "echo", "", func(_ context.Context, in testInput) (testOutput, error) { return testOutput{in.Text}, nil })
	ctx := context.Background()
	raw := []byte(`{"text":"hello","count":1}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Call(ctx, "echo", raw); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkMemoHit(b *testing.B) {
	m := NewMemo[string, int](10, time.Hour)
	ctx := context.Background()
	load := func(context.Context) (int, error) { return 1, nil }
	_, _ = m.Get(ctx, "key", load)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Get(ctx, "key", load); err != nil {
			b.Fatal(err)
		}
	}
}
