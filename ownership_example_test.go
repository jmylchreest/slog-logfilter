package logfilter_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/jmylchreest/slog-logfilter"
)

func ExampleNew() {
	var output bytes.Buffer
	logger := logfilter.New(
		logfilter.WithOutput(&output),
		logfilter.WithLevel(slog.LevelWarn),
	)
	handler := logger.Handler().(*logfilter.Handler)
	logger.Info("suppressed")
	fmt.Println(output.Len() == 0)
	handler.SetLevel(slog.LevelDebug)
	logger.Debug("emitted")
	fmt.Println(output.Len() > 0)
	fmt.Println(handler.GetLevel())
	// Output:
	// true
	// true
	// DEBUG
}

func ExampleWithContextExtractors() {
	type tenantKey struct{}
	var output bytes.Buffer
	logger := logfilter.New(
		logfilter.WithOutput(&output),
		logfilter.WithContextExtractors(map[string]logfilter.ContextExtractor{
			"tenant": func(ctx context.Context) (string, bool) {
				value, ok := ctx.Value(tenantKey{}).(string)
				return value, ok
			},
		}),
		logfilter.WithFilters([]logfilter.LogFilter{
			{Type: "context:tenant", Pattern: "debug-tenant", Level: "debug", Enabled: true},
		}),
	)
	ctx := context.WithValue(context.Background(), tenantKey{}, "debug-tenant")
	logger.DebugContext(ctx, "tenant-specific diagnostics")
	fmt.Println(output.Len() > 0)
	// Output: true
}

func ExampleNew_default() {
	previous := slog.Default()
	defer slog.SetDefault(previous)
	var output bytes.Buffer
	logger := logfilter.New(logfilter.WithOutput(&output))
	slog.SetDefault(logger)
	child := logger.With("component", "worker")
	handler := logger.Handler().(*logfilter.Handler)
	handler.SetFilters([]logfilter.LogFilter{
		{Type: "component", Pattern: "worker", Level: "debug", Enabled: true},
	})
	child.Debug("worker diagnostics")
	fmt.Println(output.Len() > 0)
	output.Reset()
	slog.Info("application message")
	fmt.Println(output.Len() > 0)
	// Output:
	// true
	// true
}
