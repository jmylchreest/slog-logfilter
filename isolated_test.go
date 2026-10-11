package logfilter

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func TestNewIsolatedDoesNotChangeGlobals(t *testing.T) {
	global := New(WithOutput(io.Discard), WithLevel(slog.LevelWarn))
	globalHandler := GetHandler()
	defaultLogger := slog.Default()
	a := NewIsolated(WithOutput(io.Discard), WithLevel(slog.LevelDebug))
	b := NewIsolated(WithOutput(io.Discard), WithLevel(slog.LevelError))
	if GetHandler() != globalHandler || GetLevel() != slog.LevelWarn || slog.Default() != defaultLogger {
		t.Fatal("isolated constructor changed globals")
	}
	ah, bh := a.Handler().(*Handler), b.Handler().(*Handler)
	ah.SetLevel(slog.LevelInfo)
	if bh.GetLevel() != slog.LevelError || GetLevel() != slog.LevelWarn {
		t.Fatal("instance level leaked")
	}
	SetLevel(slog.LevelDebug)
	SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
	if ah.GetLevel() != slog.LevelInfo || bh.GetLevel() != slog.LevelError || len(ah.GetFilters()) != 0 || len(bh.GetFilters()) != 0 {
		t.Fatal("global setters changed isolated configuration")
	}
	if global.Handler() != globalHandler {
		t.Fatal("global handler replaced")
	}
	New(WithOutput(io.Discard), WithLevel(slog.LevelError))
	if ah.GetLevel() != slog.LevelInfo {
		t.Fatal("New changed isolated level")
	}
}

func TestIsolatedContextExtractors(t *testing.T) {
	RegisterContextExtractor("tenant", func(context.Context) (string, bool) { return "global", true })
	defer UnregisterContextExtractor("tenant")
	filters := []LogFilter{{Type: "context:tenant", Pattern: "local", Level: "debug", Enabled: true}}
	extractors := map[string]ContextExtractor{"tenant": func(context.Context) (string, bool) { return "local", true }}
	option := WithContextExtractors(extractors)
	extractors["tenant"] = func(context.Context) (string, bool) { return "mutated", true }
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		logger := NewIsolated(WithOutput(&buf), WithSource(false), WithFormat(format), WithFilters(filters), option)
		logger.With("id", 1).WithGroup("g").Debug("local extractor")
		if buf.Len() == 0 {
			t.Fatal("local registry lost or aliased")
		}
		buf.Reset()
		NewIsolated(WithOutput(&buf), WithFilters([]LogFilter{{Type: "context:tenant", Pattern: "global", Level: "debug", Enabled: true}})).Debug("global registry must not apply")
		if buf.Len() != 0 {
			t.Fatal("isolated logger used global registry")
		}
		buf.Reset()
		New(WithOutput(&buf), WithFilters(filters), option).Debug("explicit local registry")
		if buf.Len() == 0 {
			t.Fatal("New ignored explicit local registry")
		}
	}
}

func TestDerivedHandlerConfigurationContract(t *testing.T) {
	logger := NewIsolated(WithOutput(io.Discard))
	parent := logger.Handler().(*Handler)
	initial := LogFilter{Type: "id", Pattern: "old", Level: "debug", Enabled: true}
	parent.SetFilters([]LogFilter{initial})
	children := []*Handler{
		logger.With("id", "old").Handler().(*Handler),
		logger.WithGroup("g").Handler().(*Handler),
	}
	parent.ClearFilters()
	parent.SetLevel(slog.LevelError)
	for _, child := range children {
		if got := child.GetFilters(); len(got) != 1 || got[0].Pattern != "old" {
			t.Fatal("child filter snapshot changed")
		}
		if child.GetLevel() != slog.LevelError {
			t.Fatal("derived level not shared")
		}
		child.SetFilters([]LogFilter{{Type: "id", Pattern: "child", Enabled: true}})
		if len(parent.GetFilters()) != 0 {
			t.Fatal("child mutation changed parent")
		}
	}
	children[0].SetLevel(slog.LevelWarn)
	if parent.GetLevel() != slog.LevelWarn || children[1].GetLevel() != slog.LevelWarn {
		t.Fatal("level changes must reach all derived handlers")
	}
}

func TestLegacyNewStillSharesLevelAndTargetsLatestHandler(t *testing.T) {
	a := New(WithOutput(io.Discard), WithLevel(slog.LevelWarn)).Handler().(*Handler)
	b := New(WithOutput(io.Discard), WithLevel(slog.LevelDebug)).Handler().(*Handler)
	if a.GetLevel() != slog.LevelDebug || b.GetLevel() != slog.LevelDebug {
		t.Fatal("legacy shared level behaviour changed")
	}
	SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
	if len(a.GetFilters()) != 0 || len(b.GetFilters()) != 1 {
		t.Fatal("legacy filter target changed")
	}
}

func TestConcurrentIsolatedLoggers(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				logger := NewIsolated(WithOutput(io.Discard))
				h := logger.Handler().(*Handler)
				h.SetLevel(slog.LevelDebug)
				h.SetFilters([]LogFilter{{Type: "id", Pattern: "*", Enabled: true}})
				logger.With("id", "job").Info("message")
			}
		}()
	}
	wg.Wait()
}

func TestExplicitEmptyContextRegistry(t *testing.T) {
	RegisterContextExtractor("empty_registry_test", func(context.Context) (string, bool) {
		t.Fatal("empty local registry fell back to global registry")
		return "value", true
	})
	defer UnregisterContextExtractor("empty_registry_test")
	for _, newLogger := range []func(...Option) *slog.Logger{New, NewIsolated} {
		logger := newLogger(WithOutput(io.Discard), WithContextExtractors(nil), WithFilters([]LogFilter{
			{Type: "context:empty_registry_test", Pattern: "*", Level: "debug", Enabled: true},
		}))
		logger.Debug("no global lookup")
	}
}
