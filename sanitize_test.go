package logfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
)

type secretValue struct{}

func (secretValue) LogValue() slog.Value {
	return slog.GroupValue(slog.String("password", "secret-value"), slog.Int("count", 2))
}

type opaqueSecret struct {
	Password string
	Count    int
}
type badJSON struct{}

func (badJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("secret-value") }

func TestSanitizerPipeline(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			r := NewRedactor(RedactorOptions{Patterns: []*regexp.Regexp{regexp.MustCompile(`CUSTOM-[a-z]+`)}})
			l := New(WithOutput(&out), WithFormat(format), WithSource(false), WithSanitizer(r))
			child := l.With("access_token", "secret-value").WithGroup("request").With("lazy", secretValue{})
			child.Info("failed Bearer abc123 CUSTOM-secret", "object", opaqueSecret{"secret-value", 3}, "map", map[string]any{"client_secret": "secret-value", "nested": []any{map[string]string{"Authorization": "secret-value"}}}, "error", errors.New("https://alice:secret-value@example.com/x?api_key=secret-value&count=2"), "bad", badJSON{})
			raw := out.String()
			for _, secret := range []string{"secret-value", "abc123", "CUSTOM-secret"} {
				if strings.Contains(raw, secret) {
					t.Fatalf("leak %q: %s", secret, raw)
				}
			}
			if !strings.Contains(raw, "count") || !strings.Contains(raw, "example.com") {
				t.Fatalf("lost diagnostics: %s", raw)
			}
			if format == "json" {
				var v any
				if err := json.Unmarshal(out.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestSanitizerFilterBeforeRedaction(t *testing.T) {
	var out bytes.Buffer
	l := New(WithOutput(&out), WithSource(false), WithSanitizer(NewRedactor(RedactorOptions{})))
	child := l.With("token", "match-me")
	SetFilters([]LogFilter{{Type: "token", Pattern: "match-me", Level: "debug", OutputLevel: "info", Enabled: true}})
	child.Debug("enabled")
	if !strings.Contains(out.String(), "enabled") || strings.Contains(out.String(), "match-me") {
		t.Fatal(out.String())
	}
	out.Reset()
	ClearFilters()
	child.Debug("disabled")
	if out.Len() != 0 {
		t.Fatal(out.String())
	}
}
func TestRedactorText(t *testing.T) {
	r := NewRedactor(RedactorOptions{})
	for _, s := range []string{`Authorization: Bearer secret-value`, `password="secret-value"`, `{"refresh_token":"secret-value"}`, `https://user:secret-value@example.com/?access%5Ftoken=secret-value&ok=1`, "-----BEGIN PRIVATE KEY-----\nsecret-value\n-----END PRIVATE KEY-----"} {
		if got := r.SanitizeText(s); strings.Contains(got, "secret-value") {
			t.Errorf("leak: %s", got)
		}
	}
	if got := r.SanitizeText("request completed in 12ms"); got != "request completed in 12ms" {
		t.Fatal(got)
	}
}
func TestRedactorCyclesAndBytes(t *testing.T) {
	r := NewRedactor(RedactorOptions{})
	m := map[string]any{}
	m["self"] = m
	for _, v := range []any{m, []byte("Bearer secret-value"), badJSON{}} {
		a := r.SanitizeAttr(nil, slog.Any("value", v))
		if strings.Contains(a.Value.String(), "secret-value") {
			t.Fatal(a)
		}
	}
}
func TestSanitizerConcurrentChildren(t *testing.T) {
	l := New(WithOutput(io.Discard), WithSanitizer(NewRedactor(RedactorOptions{})))
	child := l.With("password", "secret-value")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SetFilters([]LogFilter{{Type: "password", Pattern: "secret*", Level: "debug", Enabled: true}})
				child.Debug("message")
				ClearFilters()
			}
		}()
	}
	wg.Wait()
}
func BenchmarkSanitizedLogging(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "emitted"
		}
		b.Run(name, func(b *testing.B) {
			l := New(WithOutput(io.Discard), WithSource(false), WithSanitizer(NewRedactor(RedactorOptions{})))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if enabled {
					l.Info("request complete", "component", "gateway", "count", 2)
				} else {
					l.Debug("request complete", "count", 2)
				}
			}
		})
	}
}

func TestSanitizeErrorPreservesIdentity(t *testing.T) {
	original := errors.New("Bearer secret-value")
	wrapped := SanitizeError(fmt.Errorf("request failed: %w", original), NewRedactor(RedactorOptions{}))
	if !errors.Is(wrapped, original) {
		t.Fatal("lost cause")
	}
	for _, s := range []string{wrapped.Error(), fmt.Sprintf("%+v", wrapped), fmt.Sprintf("%#v", wrapped)} {
		if strings.Contains(s, "secret-value") {
			t.Fatal(s)
		}
	}
	if SanitizeError(nil, NewRedactor(RedactorOptions{})) != nil {
		t.Fatal("nil error changed")
	}
}
func TestSanitizerOptional(t *testing.T) {
	var out bytes.Buffer
	l := New(WithOutput(&out))
	l.Info("unchanged", "password", "plain")
	if !strings.Contains(out.String(), "plain") {
		t.Fatal(out.String())
	}
}
func BenchmarkUnsanitizedLogging(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "emitted"
		}
		b.Run(name, func(b *testing.B) {
			l := New(WithOutput(io.Discard), WithSource(false))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if enabled {
					l.Info("request complete", "component", "gateway", "count", 2)
				} else {
					l.Debug("request complete", "count", 2)
				}
			}
		})
	}
}

func TestRedactorRawQueryAndTruncatedKey(t *testing.T) {
	r := NewRedactor(RedactorOptions{})
	for _, text := range []string{"token=secret-value", "secret=secret-value", "-----BEGIN PRIVATE KEY-----\nsecret-value"} {
		if got := r.SanitizeText(text); strings.Contains(got, "secret-value") {
			t.Fatal(got)
		}
	}
}
