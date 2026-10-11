package logfilter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func BenchmarkSanitisation(b *testing.B) {
	s, err := NewSanitiser(DefaultSanitiserRules()...)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"disabled", "no_match", "matching", "groups"} {
		b.Run(mode, func(b *testing.B) {
			var h slog.Handler = slog.NewJSONHandler(io.Discard, nil)
			if mode != "disabled" {
				h = NewSanitisingHandler(h, s)
			}
			r := slog.NewRecord(time.Unix(0, 0), slog.LevelInfo, "request completed", 0)
			for i := 0; i < 16; i++ {
				a := slog.String("status", "completed")
				if mode == "matching" {
					a = slog.String("value", "id=550e8400-e29b-41d4-a716-446655440000")
				}
				if mode == "groups" {
					a = slog.Group("request", "status", "completed", "count", 3)
				}
				r.AddAttrs(a)
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := h.Handle(ctx, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
