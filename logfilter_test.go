package logfilter

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNew_DefaultOptions(t *testing.T) {
	logger := New()
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	// Default level should be INFO
	if logger.Handler().(*Handler).GetLevel() != slog.LevelInfo {
		t.Errorf("Expected default level INFO, got %v", logger.Handler().(*Handler).GetLevel())
	}
}

func TestNew_WithOptions(t *testing.T) {
	var buf bytes.Buffer
	logger := New(
		WithLevel(slog.LevelDebug),
		WithFormat("text"),
		WithOutput(&buf),
		WithSource(false),
	)

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	if logger.Handler().(*Handler).GetLevel() != slog.LevelDebug {
		t.Errorf("Expected level DEBUG, got %v", logger.Handler().(*Handler).GetLevel())
	}

	// Log something and verify output
	logger.Info("test message")
	if !strings.Contains(buf.String(), "test message") {
		t.Error("Expected log output to contain message")
	}
}

func TestNew_WithFilters(t *testing.T) {
	var buf bytes.Buffer
	filters := []LogFilter{
		{Type: "job_id", Pattern: "test_*", Level: "debug", Enabled: true},
	}

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters(filters),
	)

	// Verify filters were set
	gotFilters := logger.Handler().(*Handler).GetFilters()
	if len(gotFilters) != 1 {
		t.Errorf("Expected 1 filter, got %d", len(gotFilters))
	}
}

func TestSetLevel(t *testing.T) {
	h := New(WithLevel(slog.LevelInfo)).Handler().(*Handler)

	h.SetLevel(slog.LevelDebug)
	if h.GetLevel() != slog.LevelDebug {
		t.Errorf("Expected DEBUG after SetLevel, got %v", h.GetLevel())
	}

	h.SetLevel(slog.LevelWarn)
	if h.GetLevel() != slog.LevelWarn {
		t.Errorf("Expected WARN after SetLevel, got %v", h.GetLevel())
	}
}

func TestSetFilters_Handler(t *testing.T) {
	h := New().Handler().(*Handler)

	filters := []LogFilter{
		{Type: "a", Pattern: "1", Level: "debug", Enabled: true},
		{Type: "b", Pattern: "2", Level: "info", Enabled: true},
	}

	h.SetFilters(filters)

	got := h.GetFilters()
	if len(got) != 2 {
		t.Errorf("Expected 2 filters, got %d", len(got))
	}

	// Clear
	h.ClearFilters()
	if len(h.GetFilters()) != 0 {
		t.Error("Expected 0 filters after clear")
	}
}

func TestAddRemoveFilter_Handler(t *testing.T) {
	h := New().Handler().(*Handler)
	h.ClearFilters()

	h.AddFilter(LogFilter{Type: "x", Pattern: "y", Level: "debug", Enabled: true})
	if len(h.GetFilters()) != 1 {
		t.Error("Expected 1 filter after add")
	}

	h.RemoveFilter("x", "y")
	if len(h.GetFilters()) != 0 {
		t.Error("Expected 0 filters after remove")
	}
}

func TestSetDefault(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var buf bytes.Buffer
	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
	)

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	slog.SetDefault(logger)

	// slog.Default() should now use our logger
	slog.Info("test via default")
	if !strings.Contains(buf.String(), "test via default") {
		t.Error("Expected slog.Default to use our handler")
	}
}

func TestIntegration_FilterElevation(t *testing.T) {
	var buf bytes.Buffer

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: true},
		}),
	)

	// Debug without matching attribute - suppressed
	buf.Reset()
	logger.Debug("msg1", "job_id", "normal_123")
	if buf.Len() > 0 {
		t.Error("Expected debug without match to be suppressed")
	}

	// Debug with matching attribute - emitted
	buf.Reset()
	logger.Debug("msg2", "job_id", "debug_abc")
	if buf.Len() == 0 {
		t.Error("Expected debug with match to be emitted")
	}
	if !strings.Contains(buf.String(), "msg2") {
		t.Error("Expected message content in output")
	}
}

func TestIntegration_FilterSuppression(t *testing.T) {
	var buf bytes.Buffer

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "noisy_*", Level: "error", Enabled: true},
		}),
	)

	// Info with matching attribute - suppressed (filter sets ERROR)
	buf.Reset()
	logger.Info("msg1", "job_id", "noisy_123")
	if buf.Len() > 0 {
		t.Error("Expected info with suppression filter to be suppressed")
	}

	// Error with matching attribute - emitted
	buf.Reset()
	logger.Error("msg2", "job_id", "noisy_123")
	if buf.Len() == 0 {
		t.Error("Expected error with match to be emitted")
	}

	// Info without matching attribute - emitted (base level)
	buf.Reset()
	logger.Info("msg3", "job_id", "normal_456")
	if buf.Len() == 0 {
		t.Error("Expected info without match to be emitted")
	}
}

func TestIntegration_FilterExpiry(t *testing.T) {
	var buf bytes.Buffer

	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "expired_*", Level: "debug", Enabled: true, ExpiresAt: &past},
			{Type: "job_id", Pattern: "active_*", Level: "debug", Enabled: true, ExpiresAt: &future},
		}),
	)

	// Expired filter should not match
	buf.Reset()
	logger.Debug("msg1", "job_id", "expired_123")
	if buf.Len() > 0 {
		t.Error("Expected expired filter to not match")
	}

	// Active filter should match
	buf.Reset()
	logger.Debug("msg2", "job_id", "active_456")
	if buf.Len() == 0 {
		t.Error("Expected active filter to match")
	}
}

func TestTrimSourcePath(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		prefix   string
		workDir  string
		want     string
	}{
		{
			name:     "module prefix strips absolute build path",
			filePath: "/home/user/src/github.com/user/repo/pkg/foo/bar.go",
			prefix:   "/home/user/src/github.com/user/repo/",
			workDir:  "/",
			want:     "pkg/foo/bar.go",
		},
		{
			name:     "module prefix found mid-path",
			filePath: "/opt/build/github.com/user/repo/internal/server.go",
			prefix:   "github.com/user/repo",
			workDir:  "/run/svc",
			want:     "internal/server.go",
		},
		{
			name:     "workDir-relative path when prefix empty",
			filePath: "/home/user/project/pkg/foo.go",
			prefix:   "",
			workDir:  "/home/user/project",
			want:     "pkg/foo.go",
		},
		{
			name:     "workDir produces .. prefix, falls to basename",
			filePath: "/home/user/src/repo/pkg/foo.go",
			prefix:   "",
			workDir:  "/var/run",
			want:     "foo.go",
		},
		{
			name:     "empty prefix and workDir, falls to basename",
			filePath: "/some/absolute/path/file.go",
			prefix:   "",
			workDir:  "",
			want:     "file.go",
		},
		{
			name:     "prefix not found in path, workDir relative works",
			filePath: "/home/user/project/cmd/main.go",
			prefix:   "github.com/other/repo",
			workDir:  "/home/user/project",
			want:     "cmd/main.go",
		},
		{
			name:     "already trimmed path (no separators to strip)",
			filePath: "pkg/foo/bar.go",
			prefix:   "",
			workDir:  "",
			want:     "bar.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimSourcePath(tt.filePath, tt.prefix, tt.workDir)
			if got != tt.want {
				t.Errorf("trimSourcePath(%q, %q, %q) = %q, want %q",
					tt.filePath, tt.prefix, tt.workDir, got, tt.want)
			}
		})
	}
}

func TestSourcePathInLogOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithSource(true),
	)

	logger.Info("test source path")
	output := buf.String()

	// The source path should be relative to the module root,
	// not an absolute filesystem path.
	if strings.Contains(output, "/home/") {
		t.Errorf("Log output contains absolute path: %s", output)
	}
	// Should contain just the package-relative path
	if !strings.Contains(output, "logfilter_test.go") {
		t.Errorf("Log output missing expected filename: %s", output)
	}
}

func TestDetectSourcePrefix(t *testing.T) {
	prefix := detectSourcePrefix()
	// When running tests, the prefix should be non-empty since
	// we have valid build info from the test binary.
	if prefix == "" {
		t.Skip("No build info available (expected in some CI environments)")
	}
	// The prefix should contain the module path
	if !strings.Contains(prefix, "slog-logfilter") {
		t.Errorf("Expected prefix to contain module name, got %q", prefix)
	}
}
