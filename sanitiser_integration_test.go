package logfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/slogtest"
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

func TestSanitiserPipeline(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			r := objectSanitiser(t, SanitiserRule{Pattern: `CUSTOM-[a-z]+`})
			l := New(WithOutput(&out), WithFormat(format), WithSource(false), WithSanitiser(r))
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
func TestSanitiserFilterBeforeRedaction(t *testing.T) {
	var out bytes.Buffer
	l := New(WithOutput(&out), WithSource(false), WithSanitiser(objectSanitiser(t)))
	child := l.With("token", "match-me")
	l.Handler().(*Handler).SetFilters([]LogFilter{{Type: "token", Pattern: "match-me", Level: "debug", OutputLevel: "info", Enabled: true}})
	child.Debug("enabled")
	if !strings.Contains(out.String(), "enabled") || strings.Contains(out.String(), "match-me") {
		t.Fatal(out.String())
	}
	out.Reset()
	l.Handler().(*Handler).ClearFilters()
	child.Debug("disabled")
	if out.Len() != 0 {
		t.Fatal(out.String())
	}
}
func TestIntegratedDefaultsText(t *testing.T) {
	r := objectSanitiser(t)
	for _, s := range []string{`Authorization: Bearer secret-value`, `password="secret-value"`, `{"refresh_token":"secret-value"}`, `https://user:secret-value@example.com/?access%5Ftoken=secret-value&ok=1`, "-----BEGIN PRIVATE KEY-----\nsecret-value\n-----END PRIVATE KEY-----"} {
		if got := r.SanitiseText(s); strings.Contains(got, "secret-value") {
			t.Errorf("leak: %s", got)
		}
	}
	if got := r.SanitiseText("request completed in 12ms"); got != "request completed in 12ms" {
		t.Fatal(got)
	}
}
func TestIntegratedDefaultsCyclesAndBytes(t *testing.T) {
	r := objectSanitiser(t)
	m := map[string]any{}
	m["self"] = m
	for _, v := range []any{m, []byte("Bearer secret-value"), badJSON{}} {
		a := r.SanitiseAttr(nil, slog.Any("value", v))
		if strings.Contains(a.Value.String(), "secret-value") {
			t.Fatal(a)
		}
	}
}
func TestIntegratedSanitiserConcurrentChildren(t *testing.T) {
	l := New(WithOutput(io.Discard), WithSanitiser(objectSanitiser(t)))
	child := l.With("password", "secret-value")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Handler().(*Handler).SetFilters([]LogFilter{{Type: "password", Pattern: "secret*", Level: "debug", Enabled: true}})
				child.Debug("message")
				l.Handler().(*Handler).ClearFilters()
			}
		}()
	}
	wg.Wait()
}
func BenchmarkSanitisedLogging(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "emitted"
		}
		b.Run(name, func(b *testing.B) {
			l := New(WithOutput(io.Discard), WithSource(false), WithSanitiser(objectSanitiser(b)))
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

func TestSanitiseErrorPreservesIdentity(t *testing.T) {
	original := errors.New("Bearer secret-value")
	wrapped := SanitiseError(fmt.Errorf("request failed: %w", original), objectSanitiser(t))
	if !errors.Is(wrapped, original) {
		t.Fatal("lost cause")
	}
	for _, s := range []string{wrapped.Error(), fmt.Sprintf("%+v", wrapped), fmt.Sprintf("%#v", wrapped)} {
		if strings.Contains(s, "secret-value") {
			t.Fatal(s)
		}
	}
	if SanitiseError(nil, objectSanitiser(t)) != nil {
		t.Fatal("nil error changed")
	}
}
func TestSanitiserOptional(t *testing.T) {
	var out bytes.Buffer
	l := New(WithOutput(&out))
	l.Info("unchanged", "password", "plain")
	if !strings.Contains(out.String(), "plain") {
		t.Fatal(out.String())
	}
}
func BenchmarkUnsanitisedLogging(b *testing.B) {
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

func TestIntegratedDefaultsRawQueryAndTruncatedKey(t *testing.T) {
	r := objectSanitiser(t)
	for _, text := range []string{"token=secret-value", "secret=secret-value", "-----BEGIN PRIVATE KEY-----\nsecret-value"} {
		if got := r.SanitiseText(text); strings.Contains(got, "secret-value") {
			t.Fatal(got)
		}
	}
}

func objectSanitiser(t testing.TB, extra ...SanitiserRule) *Sanitiser {
	t.Helper()
	rules := append(DefaultSanitiserRules(), extra...)
	s, err := NewSanitiserWithConfig(SanitiserConfig{Rules: rules, SanitiseObjects: true, SanitiseKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type panickingJSON struct{}

func (panickingJSON) MarshalJSON() ([]byte, error) { panic("secret-value") }

type panickingError struct{}

func (panickingError) Error() string { panic("secret-value") }

func TestObjectSanitisationIsExplicitAndDoesNotMutate(t *testing.T) {
	input := map[string]any{"password": "secret-value", "nested": []any{"api_key=secret-value"}}
	plain := mustSanitiser(t, DefaultSanitiserRules()...)
	attr := slog.Any("object", input)
	if got := plain.SanitiseAttr(nil, attr); got.Value.Any().(map[string]any)["password"] != "secret-value" {
		t.Fatal("opaque object changed without opt-in")
	}
	configured := objectSanitiser(t)
	got := configured.SanitiseAttr(nil, attr).Value.Any().(map[string]any)
	if got["password"] != Redaction || got["nested"].([]any)[0] != Redaction {
		t.Fatalf("object not sanitised: %v", got)
	}
	if input["password"] != "secret-value" || input["nested"].([]any)[0] != "api_key=secret-value" {
		t.Fatal("caller-owned object mutated")
	}
	for _, value := range []any{panickingJSON{}, panickingError{}, make(chan int)} {
		if got := configured.SanitiseAttr(nil, slog.Any("value", value)); got.Value.String() != "[unloggable]" {
			t.Fatalf("unsafe object did not fail closed: %v", got)
		}
	}
}

func TestObjectRulesKeepKeyScopeAndCallbackPanics(t *testing.T) {
	s, err := NewSanitiserWithConfig(SanitiserConfig{
		SanitiseObjects: true,
		Rules:           []SanitiserRule{{KeyPattern: "^email$", Pattern: "@.*", Replacement: "@hidden"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := s.SanitiseAttr(nil, slog.Any("object", map[string]any{"email": "alice@example.com", "other": "alice@example.com"})).Value.Any().(map[string]any)
	if got["email"] != "alice@hidden" || got["other"] != "alice@example.com" {
		t.Fatal(got)
	}
	s, err = NewSanitiserWithConfig(SanitiserConfig{
		SanitiseObjects: true,
		Rules:           []SanitiserRule{{ReplaceFunc: func(string) string { panic("callback") }}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if got := recover(); got != "callback" {
			t.Fatalf("callback panic changed: %v", got)
		}
	}()
	s.SanitiseAttr(nil, slog.Any("object", map[string]string{"key": "value"}))
}

func TestSensitiveGroupAndKeyScanning(t *testing.T) {
	for _, bound := range []bool{false, true} {
		var out bytes.Buffer
		logger := New(WithOutput(&out), WithSource(false), WithSanitiser(objectSanitiser(t)))
		child := logger.WithGroup("credentials").WithGroup("nested")
		if bound {
			child = child.With("ordinary", "secret-value")
			child.Info("message")
		} else {
			child.Info("message", "ordinary", "secret-value")
		}
		if strings.Contains(out.String(), "secret-value") || !strings.Contains(out.String(), Redaction) {
			t.Fatal(out.String())
		}
		out.Reset()
		logger.Info("parent", "ordinary", "visible")
		if !strings.Contains(out.String(), "visible") {
			t.Fatal("child changed parent")
		}
	}
	plain := mustSanitiser(t, SanitiserRule{Pattern: "secret-value"})
	scanning, err := NewSanitiserWithConfig(SanitiserConfig{Rules: []SanitiserRule{{Pattern: "secret-value"}}, SanitiseKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.SanitiseAttr(nil, slog.Int("secret-value", 1)).Key != "secret-value" ||
		scanning.SanitiseAttr(nil, slog.Int("secret-value", 1)).Key != Redaction {
		t.Fatal("key scanning option ignored")
	}
}

func TestIntegratedCredentialCoverage(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	for _, input := range []string{
		"Basic c2VjcmV0LXZhbHVl",
		"client-secret=secret-value",
		"apiKey='secret-value'",
		"id_token=secret-value",
		"https://alice:secret-value@example.com/?access%5Ftoken=secret-value&ok=1",
		"https://example.com/?api_key=secret-value&bad=%ZZ",
		"-----BEGIN RSA PRIVATE KEY-----\nsecret-value",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-value\n-----END OPENSSH PRIVATE KEY-----",
	} {
		got := s.SanitiseText(input)
		if strings.Contains(got, "secret-value") || strings.Contains(got, "c2VjcmV0LXZhbHVl") {
			t.Fatalf("leak: %s", got)
		}
	}
	for _, key := range []string{"Proxy-Authorization", "client secret", "private_key", "set-cookie", "credentials", "apiKey"} {
		if got := s.SanitiseAttr(nil, slog.String(key, "secret-value")); got.Value.String() != Redaction {
			t.Fatal(got)
		}
	}
	safeURL := "https://example.com/path?ok=1"
	if got := s.SanitiseText(safeURL); got != safeURL {
		t.Fatal(got)
	}
}

type identifiableError struct{ text string }

func (e *identifiableError) Error() string { return e.text }

func TestSanitiseErrorCompositionAndAs(t *testing.T) {
	original := &identifiableError{text: "secret"}
	first := mustSanitiser(t, SanitiserRule{Pattern: "secret", Replacement: "first"})
	second := mustSanitiser(t, SanitiserRule{Pattern: "first", Replacement: "last"})
	wrapped := SanitiseError(original, nil, first, second)
	var target *identifiableError
	if !errors.As(wrapped, &target) || target != original || errors.Unwrap(wrapped) != original {
		t.Fatal("error identity lost")
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		got := fmt.Sprintf(format, wrapped)
		if !strings.Contains(got, "last") || strings.Contains(got, "secret") {
			t.Fatal(got)
		}
	}
	if SanitiseError(original, nil, &Sanitiser{}) != original {
		t.Fatal("inactive wrapper changed identity")
	}
}

func TestIntegratedSlogHandlerContract(t *testing.T) {
	var output bytes.Buffer
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	handler := NewHandler(NewSanitisingHandler(slog.NewJSONHandler(&output, nil), s), new(slog.LevelVar))
	err := slogtest.TestHandler(handler, func() []map[string]any {
		decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
		var records []map[string]any
		for {
			var record map[string]any
			err := decoder.Decode(&record)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, record)
		}
		return records
	})
	if err != nil {
		t.Fatal(err)
	}
}
