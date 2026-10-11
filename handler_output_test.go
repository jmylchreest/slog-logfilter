package logfilter

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

type outputLevelCapture struct{ record slog.Record }

func (h *outputLevelCapture) Enabled(context.Context, slog.Level) bool      { return true }
func (h *outputLevelCapture) Handle(_ context.Context, r slog.Record) error { h.record = r; return nil }
func (h *outputLevelCapture) WithAttrs([]slog.Attr) slog.Handler            { return h }
func (h *outputLevelCapture) WithGroup(string) slog.Handler                 { return h }

func TestOutputLevelPreservesOriginalRecord(t *testing.T) {
	inner := &outputLevelCapture{}
	h := NewHandler(inner, new(slog.LevelVar))
	h.SetFilters([]LogFilter{{Type: "job_id", Pattern: "job", Level: "debug", OutputLevel: "warn", Enabled: true}})
	r := slog.NewRecord(time.Unix(123, 0), slog.LevelDebug, "message", 42)
	r.AddAttrs(slog.String("job_id", "job"))
	for i := 0; i < 15; i++ {
		r.AddAttrs(slog.Int("count", i))
	}
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Level != slog.LevelDebug || inner.record.Level != slog.LevelWarn {
		t.Fatal("level mutation escaped handler")
	}
	if inner.record.Time != r.Time || inner.record.PC != r.PC || inner.record.Message != r.Message || inner.record.NumAttrs() != r.NumAttrs() {
		t.Fatal("record metadata changed")
	}
}
