package logfilter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func BenchmarkOutputLevel(b *testing.B) {
	level := new(slog.LevelVar)
	h := NewHandler(slog.NewJSONHandler(io.Discard, nil), level)
	h.SetFilters([]LogFilter{{Type: "job_id", Pattern: "debug", Level: "debug", OutputLevel: "info", Enabled: true}})
	r := slog.NewRecord(time.Unix(0, 0), slog.LevelDebug, "request", 0)
	r.AddAttrs(slog.String("job_id", "debug"))
	for i := 0; i < 15; i++ {
		r.AddAttrs(slog.Int("count", i))
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := h.Handle(ctx, r); err != nil {
			b.Fatal(err)
		}
	}
}
