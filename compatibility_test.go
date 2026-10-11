package logfilter

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestDisabledSanitisationPreservesOutput(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		for _, option := range []Option{func(*options) {}, WithSanitiser(nil), WithSanitisers(), WithSanitisers(&Sanitiser{})} {
			var want, got bytes.Buffer
			level := new(slog.LevelVar)
			var legacyInner slog.Handler = slog.NewJSONHandler(&want, &slog.HandlerOptions{Level: level})
			if format == "text" {
				legacyInner = slog.NewTextHandler(&want, &slog.HandlerOptions{Level: level})
			}
			legacy := NewHandler(legacyInner, level)
			filters := []LogFilter{{Type: "username", Pattern: "alice", Level: "debug", OutputLevel: "warn", Enabled: true}}
			legacy.SetFilters(filters)
			current := New(WithFormat(format), WithSource(false), WithOutput(&got), WithFilters(filters), option)
			r := slog.NewRecord(time.Unix(123, 0), slog.LevelDebug, "password=secret 4111111111111111", 0)
			r.AddAttrs(slog.String("username", "alice"), slog.Group("nested", "api_key", "secret"))
			for i := 0; i < 12; i++ {
				r.AddAttrs(slog.Int("number", i))
			}
			for _, h := range []slog.Handler{legacy, current.Handler()} {
				h = h.WithAttrs([]slog.Attr{slog.String("password", "bound-secret")}).WithGroup("request")
				if !h.Enabled(context.Background(), r.Level) {
					t.Fatal("legacy debug filter disabled")
				}
				if err := h.Handle(context.Background(), r); err != nil {
					t.Fatal(err)
				}
			}
			if got.String() != want.String() {
				t.Fatalf("%s output changed:\ngot %s\nwant %s", format, &got, &want)
			}
		}
	}
}
