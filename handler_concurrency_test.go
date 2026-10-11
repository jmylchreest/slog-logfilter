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
	h.AddFilter(LogFilter{Type: "context:request", Pattern: "*", Level: "debug", Enabled: true})
	child := h.WithAttrs([]slog.Attr{slog.String("job_id", "job")})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			h.RegisterContextExtractor("request", func(context.Context) (string, bool) { return "value", true })
			h.AddFilter(LogFilter{Type: "other", Pattern: "*", Enabled: true})
			h.RemoveFilter("other", "*")
			h.UnregisterContextExtractor("request")
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
			r.AddAttrs(slog.String("job_id", "other"))
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

func TestFilterExpiryOwnership(t *testing.T) {
	h := NewHandler(slog.NewJSONHandler(io.Discard, nil), new(slog.LevelVar))
	expiry := time.Now().Add(time.Hour)
	want := expiry
	h.SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true, ExpiresAt: &expiry}})
	h.AddFilter(LogFilter{Type: "other", Pattern: "*", Enabled: true, ExpiresAt: &expiry})
	expiry = time.Time{}
	copy := h.GetFilters()
	for i := range copy {
		if !copy[i].ExpiresAt.Equal(want) {
			t.Fatal("caller changed installed expiry")
		}
		*copy[i].ExpiresAt = time.Time{}
	}
	for _, filter := range h.GetFilters() {
		if !filter.ExpiresAt.Equal(want) {
			t.Fatal("GetFilters exposed live expiry pointer")
		}
	}
}
