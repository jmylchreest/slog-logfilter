package logfilter

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestNewDoesNotChangeExistingLoggers(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	a := New(WithOutput(io.Discard), WithLevel(slog.LevelWarn))
	slog.SetDefault(a)
	b := New(WithOutput(io.Discard), WithLevel(slog.LevelError))
	ah, bh := a.Handler().(*Handler), b.Handler().(*Handler)
	bh.SetLevel(slog.LevelDebug)
	bh.SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
	bh.RegisterContextExtractor("id", func(context.Context) (string, bool) { return "b", true })
	if slog.Default() != a || ah.GetLevel() != slog.LevelWarn || len(ah.GetFilters()) != 0 || ah.GetContextExtractor("id") != nil {
		t.Fatal("independent configuration changed existing logger or slog default")
	}
	slog.SetDefault(b)
	ah.SetLevel(slog.LevelInfo)
	if bh.GetLevel() != slog.LevelDebug {
		t.Fatal("replacing slog default linked independent levels")
	}
}

func TestIsolatedContextExtractors(t *testing.T) {
	other := New(WithOutput(io.Discard)).Handler().(*Handler)
	other.RegisterContextExtractor("tenant", func(context.Context) (string, bool) { return "other", true })
	filters := []LogFilter{{Type: "context:tenant", Pattern: "local", Level: "debug", Enabled: true}}
	extractors := map[string]ContextExtractor{"tenant": func(context.Context) (string, bool) { return "local", true }}
	option := WithContextExtractors(extractors)
	extractors["tenant"] = func(context.Context) (string, bool) { return "mutated", true }
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		logger := New(WithOutput(&buf), WithSource(false), WithFormat(format), WithFilters(filters), option)
		logger.With("id", 1).WithGroup("g").Debug("local extractor")
		if buf.Len() == 0 {
			t.Fatal("local registry lost or aliased")
		}
		buf.Reset()
		New(WithOutput(&buf), WithFilters([]LogFilter{{Type: "context:tenant", Pattern: "other", Level: "debug", Enabled: true}})).Debug("another logger registry must not apply")
		if buf.Len() != 0 {
			t.Fatal("isolated logger used another logger registry")
		}
		buf.Reset()
		New(WithOutput(&buf), WithFilters(filters), option).Debug("explicit local registry")
		if buf.Len() == 0 {
			t.Fatal("New ignored explicit local registry")
		}
	}
}

func TestDerivedHandlerConfigurationContract(t *testing.T) {
	var buf bytes.Buffer
	logger := New(WithOutput(&buf), WithSource(false))
	parent := logger.Handler().(*Handler)
	children := []*slog.Logger{logger.With("id", "job"), logger.WithGroup("g")}
	parent.SetFilters([]LogFilter{{Type: "id", Pattern: "job", Level: "debug", Enabled: true}})
	for _, child := range children {
		buf.Reset()
		child.Debug("enabled after child creation", "id", "job")
		if buf.Len() == 0 {
			t.Fatal("existing child did not receive filter update")
		}
	}
	children[0].Handler().(*Handler).ClearFilters()
	for _, child := range children {
		buf.Reset()
		child.Debug("now suppressed", "id", "job")
		if buf.Len() != 0 {
			t.Fatal("child mutation did not reach sibling")
		}
	}
	if len(parent.GetFilters()) != 0 {
		t.Fatal("child mutation did not reach parent")
	}
	parent.SetLevel(slog.LevelError)
	for _, child := range children {
		if child.Handler().(*Handler).GetLevel() != slog.LevelError {
			t.Fatal("level not shared")
		}
	}
	children[0].Handler().(*Handler).SetLevel(slog.LevelWarn)
	if parent.GetLevel() != slog.LevelWarn {
		t.Fatal("child level update not shared")
	}
}

func TestConcurrentIsolatedLoggers(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				logger := New(WithOutput(io.Discard))
				h := logger.Handler().(*Handler)
				h.SetLevel(slog.LevelDebug)
				h.SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
				logger.With("id", "job").Info("message")
			}
		}()
	}
	wg.Wait()
}

func TestContextUpdatesReachFamilyWithoutCrossingInstances(t *testing.T) {
	extractors := map[string]ContextExtractor{"id": func(context.Context) (string, bool) { return "initial", true }}
	option := WithContextExtractors(extractors)
	var buf bytes.Buffer
	a := New(WithOutput(&buf), option, WithFilters([]LogFilter{{Type: "context:id", Pattern: "updated", Level: "debug", Enabled: true}}))
	b := New(WithOutput(io.Discard), option).Handler().(*Handler)
	child := a.WithGroup("g").With("bound", true)
	ah := a.Handler().(*Handler)
	child.Handler().(*Handler).RegisterContextExtractor("id", func(context.Context) (string, bool) {
		// Re-entrant access must not deadlock under a held registry lock.
		ah.GetContextExtractor("id")
		return "updated", true
	})
	child.Debug("updated extractor")
	if buf.Len() == 0 {
		t.Fatal("existing child did not use updated registry")
	}
	if value, _ := b.GetContextExtractor("id")(context.Background()); value != "initial" {
		t.Fatal("reused option shared mutable registry")
	}
	if value, _ := extractors["id"](context.Background()); value != "initial" {
		t.Fatal("input registry mutated")
	}
	ah.UnregisterContextExtractor("id")
	if child.Handler().(*Handler).GetContextExtractor("id") != nil {
		t.Fatal("unregister did not reach child")
	}
}

// Sharing a LevelVar is explicit; other configuration stays with its handler.
func TestNewHandlerSharesOnlySuppliedLevel(t *testing.T) {
	level := new(slog.LevelVar)
	inner := slog.NewJSONHandler(io.Discard, nil)
	a, b := NewHandler(inner, level), NewHandler(inner, level)
	a.SetLevel(slog.LevelWarn)
	a.SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
	a.RegisterContextExtractor("id", func(context.Context) (string, bool) { return "a", true })
	if b.GetLevel() != slog.LevelWarn || len(b.GetFilters()) != 0 || b.GetContextExtractor("id") != nil {
		t.Fatal("NewHandler must share only the explicitly supplied level")
	}
}

func TestWithFiltersSnapshotsInputAndSupportsReuse(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	wantExpiry := expiry
	filters := []LogFilter{{Type: "id", Pattern: "original", Level: "debug", Enabled: true, ExpiresAt: &expiry}}
	option := WithFilters(filters)
	filters[0].Pattern = "mutated"
	expiry = time.Time{}
	a := New(WithOutput(io.Discard), option).Handler().(*Handler)
	a.ClearFilters()
	b := New(WithOutput(io.Discard), option).Handler().(*Handler)
	got := b.GetFilters()
	if len(got) != 1 || got[0].Pattern != "original" || got[0].ExpiresAt == nil || !got[0].ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("option did not preserve its original configuration: %+v", got)
	}
}
