package logfilter

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestConcurrentFilterUpdatesAndDerivation(t *testing.T) {
	h := NewHandler(slog.NewJSONHandler(io.Discard, nil), new(slog.LevelVar))
	h.SetFilters([]LogFilter{{Type: "job_id", Pattern: "job", Level: "debug", Enabled: true}})
	child := h.WithAttrs([]slog.Attr{slog.String("job_id", "job")})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			h.AddFilter(LogFilter{Type: "other", Pattern: "*", Enabled: true})
			h.RemoveFilter("other", "*")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			h.WithAttrs([]slog.Attr{slog.Int("count", i)}).WithGroup("group")
			h.WithGroup("group")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r := slog.NewRecord(time.Time{}, slog.LevelInfo, "test", 0)
			r.AddAttrs(slog.String("job_id", "job"))
			if err := h.Handle(context.Background(), r); err != nil {
				t.Error(err)
			}
			if err := child.Handle(context.Background(), r); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
}
