package logfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustSanitiser(t *testing.T, rules ...SanitiserRule) *Sanitiser {
	t.Helper()
	s, err := NewSanitiser(rules...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSanitiserDefaults(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(WithOutput(&buf), WithSource(false), WithFormat(format), WithSanitiser(s))
			logger.With("username", "alice").WithGroup("request").Info(
				"id=550e8400-e29b-41d4-a716-446655440000 password=\"two words\" Authorization: Bearer abc.def.ghi card=4111-1111-1111-1111",
				"password", "hunter2", "card_number", int64(4111111111111111),
				slog.Group("nested", "API_KEY", "key-secret", "ok", "visible"))
			out := buf.String()
			for _, secret := range []string{"alice", "550e8400", "two words", "abc.def.ghi", "4111", "hunter2", "key-secret"} {
				if strings.Contains(out, secret) {
					t.Errorf("leaked %q: %s", secret, out)
				}
			}
			if !strings.Contains(out, Redaction) || !strings.Contains(out, "visible") {
				t.Fatal(out)
			}
		})
	}
}

func TestSanitiserRules(t *testing.T) {
	for _, rule := range []SanitiserRule{{}, {Pattern: "["}, {KeyPattern: "["}} {
		if s, err := NewSanitiser(rule); err == nil || s != nil {
			t.Fatalf("accepted invalid rule: %+v", rule)
		}
	}
	s := mustSanitiser(t,
		SanitiserRule{KeyPattern: "^private$", Replacement: "$1"},
		SanitiserRule{KeyPattern: "^email$", Pattern: "@.*", Replacement: "@hidden"},
		SanitiserRule{Pattern: "secret", Replacement: "masked"},
	)
	for _, tc := range []struct{ in, want slog.Attr }{
		{slog.Int("private", 42), slog.String("private", "$1")},
		{slog.Group("private", "value", "sensitive"), slog.String("private", "$1")},
		{slog.String("email", "alice@example.com"), slog.String("email", "alice@hidden")},
		{slog.String("other", "alice@example.com secret"), slog.String("other", "alice@example.com masked")},
		{slog.Int("count", 42), slog.Int("count", 42)},
		{slog.Attr{}, slog.Attr{}},
	} {
		if got := s.attr(tc.in); !got.Equal(tc.want) {
			t.Errorf("got %v, want %v", got, tc.want)
		}
	}
}

type sanitiserLogValue struct{}

func (sanitiserLogValue) LogValue() slog.Value {
	return slog.GroupValue(slog.String("password", "hidden"), slog.String("safe", "retained"))
}

func TestSanitiserGroupsAndOriginals(t *testing.T) {
	var buf bytes.Buffer
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	h := NewSanitisingHandler(slog.NewJSONHandler(&buf, nil), s)
	attrs := []slog.Attr{slog.Group("account", "password", "original")}
	child := slog.New(h.WithAttrs(attrs)).WithGroup("request").With("user_id", "alice")
	child.Info("message", slog.Any("resolved", sanitiserLogValue{}), slog.Group("", "username", "bob"), "count", 2)
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["account"].(map[string]any)["password"] != Redaction {
		t.Fatal(out)
	}
	req := out["request"].(map[string]any)
	if req["user_id"] != Redaction || req["username"] != Redaction || req["count"] != float64(2) {
		t.Fatal(out)
	}
	resolved := req["resolved"].(map[string]any)
	if resolved["password"] != Redaction || resolved["safe"] != "retained" {
		t.Fatal(out)
	}
	if attrs[0].Value.Group()[0].Value.String() != "original" {
		t.Fatal("mutated input group")
	}
	buf.Reset()
	slog.New(h).Info("parent")
	if strings.Contains(buf.String(), "account") || strings.Contains(buf.String(), "request") {
		t.Fatal("child mutated parent")
	}
}

func TestSanitiserAfterFiltering(t *testing.T) {
	for _, bound := range []bool{false, true} {
		var buf bytes.Buffer
		logger := New(WithOutput(&buf), WithSource(false), WithSanitiser(mustSanitiser(t, DefaultSanitiserRules()...)),
			WithFilters([]LogFilter{{Type: "username", Pattern: "alice", Level: "debug", OutputLevel: "warn", Enabled: true}}))
		if bound {
			logger.With("username", "alice").Debug("message")
		} else {
			logger.Debug("message", "username", "alice")
		}
		if !strings.Contains(buf.String(), "WARN") || !strings.Contains(buf.String(), Redaction) || strings.Contains(buf.String(), "alice") {
			t.Fatal(buf.String())
		}
	}
}

type sanitiserCapture struct {
	record slog.Record
	err    error
}

func (h *sanitiserCapture) Enabled(context.Context, slog.Level) bool      { return false }
func (h *sanitiserCapture) Handle(_ context.Context, r slog.Record) error { h.record = r; return h.err }
func (h *sanitiserCapture) WithAttrs([]slog.Attr) slog.Handler            { return h }
func (h *sanitiserCapture) WithGroup(string) slog.Handler                 { return h }

func TestSanitiserHandlerContract(t *testing.T) {
	inner := &sanitiserCapture{err: errors.New("write failed")}
	if NewSanitisingHandler(inner, nil) != inner || NewSanitisingHandler(inner, &Sanitiser{}) != inner {
		t.Fatal("disabled sanitiser should return inner")
	}
	h := NewSanitisingHandler(inner, mustSanitiser(t, SanitiserRule{Pattern: "secret"}))
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "secret", 123)
	r.AddAttrs(slog.String("value", "secret"))
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled not delegated")
	}
	if err := h.Handle(context.Background(), r); err != inner.err {
		t.Fatal(err)
	}
	if inner.record.Time != r.Time || inner.record.PC != r.PC || inner.record.Level != r.Level || inner.record.Message != Redaction {
		t.Fatal(inner.record)
	}
	if r.Message != "secret" {
		t.Fatal("mutated record")
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Value.String() != "secret" {
			t.Fatal("mutated attribute")
		}
		return true
	})
}

func TestSanitiserConcurrent(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	custom := mustSanitiser(t, SanitiserRule{Pattern: "REDACTED", Replacement: "PRIVATE"})
	h := NewSanitisingHandler(slog.NewJSONHandler(io.Discard, nil), s, custom)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				slog.New(h).WithGroup("g").With("username", "alice").Info("password=secret")
			}
		}()
	}
	wg.Wait()
}

func TestMultipleSanitisers(t *testing.T) {
	credentials := mustSanitiser(t, SanitiserRule{KeyPattern: "^password$", Replacement: "[CREDENTIAL]"})
	identities := mustSanitiser(t, SanitiserRule{Pattern: "alice", Replacement: "[PERSON]"})
	final := mustSanitiser(t, SanitiserRule{Pattern: `\[PERSON\]`, Replacement: "[PRIVATE]"})
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(WithOutput(&buf), WithSource(false), WithFormat(format),
				WithSanitisers(credentials, nil, &Sanitiser{}, identities, final),
				WithFilters([]LogFilter{{Type: "username", Pattern: "alice", Level: "debug", Enabled: true}}))
			logger.With("username", "alice").WithGroup("request").With("password", "secret").Debug(
				"alice", slog.Group("nested", "owner", "alice", "password", "secret"))
			out := buf.String()
			if strings.Contains(out, "alice") || strings.Contains(out, "secret") || strings.Contains(out, "[PERSON]") {
				t.Fatalf("sanitisers not applied in order: %s", out)
			}
			if strings.Count(out, "[PRIVATE]") != 3 || strings.Count(out, "[CREDENTIAL]") != 2 {
				t.Fatalf("missing sanitised values: %s", out)
			}
		})
	}
}

func TestSanitiserConfigurationsAreIndependent(t *testing.T) {
	first := mustSanitiser(t, SanitiserRule{Pattern: "secret", Replacement: "FIRST"})
	second := mustSanitiser(t, SanitiserRule{Pattern: "secret", Replacement: "SECOND"})
	configs := []*Sanitiser{first}
	option := WithSanitisers(configs...)
	configs[0] = second
	var a, b bytes.Buffer
	loggerA := New(WithOutput(&a), WithSource(false), option)
	loggerB := New(WithOutput(&b), WithSource(false), WithSanitisers(second))
	loggerA.Info("secret")
	loggerB.Info("secret")
	if !strings.Contains(a.String(), "FIRST") || !strings.Contains(b.String(), "SECOND") {
		t.Fatalf("configurations interfered: %s / %s", a.String(), b.String())
	}
	for _, override := range []Option{WithSanitisers(), WithSanitiser(nil), WithSanitisers(nil, &Sanitiser{})} {
		a.Reset()
		New(WithOutput(&a), WithSource(false), option, override).Info("secret")
		if !strings.Contains(a.String(), "secret") {
			t.Fatal("empty option did not disable sanitisation")
		}
	}
	a.Reset()
	New(WithOutput(&a), WithSource(false), option, WithSanitiser(second)).Info("secret")
	if !strings.Contains(a.String(), "SECOND") {
		t.Fatal("last option did not replace configuration")
	}
	inner := slog.NewJSONHandler(io.Discard, nil)
	if NewSanitisingHandler(inner) != inner || NewSanitisingHandler(inner, nil, &Sanitiser{}) != inner {
		t.Fatal("empty chain should return inner unchanged")
	}
}

func TestSanitiserPatternBoundaries(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	for _, tc := range []struct{ input, want string }{
		{"4111111111111111", Redaction},
		{"4111 1111 1111 1111", Redaction},
		{"1234567890123", "1234567890123"},
		{"1234567890123456789", "1234567890123456789"},
		{"123456789012", "123456789012"},
		{"12345678901234567890", "12345678901234567890"},
		{"550E8400-E29B-41D4-A716-446655440000", Redaction},
		{"username='Alice Smith'", Redaction},
		{"ordinary text", "ordinary text"},
	} {
		if got := s.attr(slog.String("value", tc.input)).Value.String(); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitiserOptInAndRuleIsolation(t *testing.T) {
	var buf bytes.Buffer
	New(WithOutput(&buf), WithSource(false)).Info("password=secret", "username", "alice")
	if !strings.Contains(buf.String(), "secret") || !strings.Contains(buf.String(), "alice") {
		t.Fatal("sanitisation enabled by default")
	}
	rules := []SanitiserRule{{Pattern: "secret", Replacement: "first"}, {Pattern: "first", Replacement: "$1"}}
	s := mustSanitiser(t, rules...)
	rules[0].Pattern = "different"
	if got := s.attr(slog.String("value", "secret")).Value.String(); got != "$1" {
		t.Fatalf("rule ordering, copying or literal replacement failed: %q", got)
	}
	buf.Reset()
	New(WithOutput(&buf), WithSource(false), WithSanitiser(mustSanitiser(t,
		SanitiserRule{KeyPattern: "^email$", Pattern: "secret"}))).Info("secret", "other", "secret", "email", "secret")
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["msg"] != "secret" || out["other"] != "secret" || out["email"] != Redaction {
		t.Fatal(out)
	}
}

func TestSanitiserCallbacks(t *testing.T) {
	if _, err := NewSanitiser(SanitiserRule{ReplaceFunc: strings.ToUpper, Replacement: "x"}); err == nil {
		t.Fatal("accepted ambiguous replacement")
	}
	s := mustSanitiser(t,
		SanitiserRule{Pattern: `secret|keep|remove`, ReplaceFunc: func(match string) string {
			switch match {
			case "secret":
				return "$1"
			case "remove":
				return ""
			default:
				return match
			}
		}},
		SanitiserRule{KeyPattern: "^upper$", ReplaceFunc: strings.ToUpper},
	)
	var buf bytes.Buffer
	logger := New(WithOutput(&buf), WithSource(false), WithSanitiser(s))
	logger.With("bound", "secret").WithGroup("g").Info("secret keep remove",
		"upper", "hello secret", "count", 5, slog.Group("nested", "value", "secret"))
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	g := out["g"].(map[string]any)
	if out["msg"] != "$1 keep " || out["bound"] != "$1" || g["upper"] != "HELLO $1" || g["count"] != float64(5) || g["nested"].(map[string]any)["value"] != "$1" {
		t.Fatal(out)
	}
	whole := mustSanitiser(t, SanitiserRule{ReplaceFunc: strings.ToUpper})
	buf.Reset()
	slog.New(NewSanitisingHandler(slog.NewJSONHandler(&buf, nil), whole)).Info("hello", "value", "world")
	if !strings.Contains(buf.String(), "HELLO") || !strings.Contains(buf.String(), "WORLD") {
		t.Fatal(buf.String())
	}
	// Whole-field rules retain precedence over callbacks, even when listed later.
	protected := mustSanitiser(t, SanitiserRule{ReplaceFunc: func(string) string { t.Fatal("callback received secret"); return "" }}, SanitiserRule{KeyPattern: "^password$"})
	if got := protected.attr(slog.String("password", "secret")).Value.String(); got != Redaction {
		t.Fatal(got)
	}
}

func TestDefaultCardValidation(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	for _, tc := range []struct {
		input  string
		redact bool
	}{
		{"4111111111111111", true},
		{"4111-1111-1111-1111", true},
		{"3782 822463 10005", true},
		{"4222222222222", true},
		{"4000000000000000006", true},
		{"4111111111111112", false},
		{"0000000000000000", false},
		{"1234567890123", false},
	} {
		want := tc.input
		if tc.redact {
			want = Redaction
		}
		if got := s.attr(slog.String("value", tc.input)).Value.String(); got != want {
			t.Errorf("%s: got %s, want %s", tc.input, got, want)
		}
	}
	if got := s.attr(slog.String("card_number", "invalid card")).Value.String(); got != Redaction {
		t.Fatal(got)
	}
	var buf bytes.Buffer
	slog.New(NewSanitisingHandler(slog.NewTextHandler(&buf, nil), s)).Info("valid=4111111111111111 invalid=4111111111111112")
	if strings.Contains(buf.String(), "4111111111111111") || !strings.Contains(buf.String(), "4111111111111112") {
		t.Fatal(buf.String())
	}
}

func TestSanitiserCopyOnChange(t *testing.T) {
	for _, position := range []int{0, 4, 8, 15} {
		t.Run(fmt.Sprint(position), func(t *testing.T) {
			inner := &sanitiserCapture{}
			s := mustSanitiser(t, SanitiserRule{Pattern: "secret"})
			h := NewSanitisingHandler(inner, s)
			r := slog.NewRecord(time.Time{}, slog.LevelInfo, "message", 0)
			for i := 0; i < 16; i++ {
				value := "safe"
				if i == position {
					value = "secret"
				}
				r.AddAttrs(slog.Group(fmt.Sprint(i), "value", value))
			}
			if err := h.Handle(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			index := 0
			inner.record.Attrs(func(a slog.Attr) bool {
				want := "safe"
				if index == position {
					want = Redaction
				}
				if a.Key != fmt.Sprint(index) || a.Value.Group()[0].Value.String() != want {
					t.Fatalf("attribute %d: %v", index, a)
				}
				index++
				return true
			})
			if index != 16 {
				t.Fatalf("lost attributes: %d", index)
			}
			index = 0
			r.Attrs(func(a slog.Attr) bool {
				want := "safe"
				if index == position {
					want = "secret"
				}
				if a.Value.Group()[0].Value.String() != want {
					t.Fatal("mutated caller's group")
				}
				index++
				return true
			})
		})
	}
}

func TestDefaultCredentialsWithEscapedQuotes(t *testing.T) {
	s := mustSanitiser(t, DefaultSanitiserRules()...)
	for _, value := range []string{
		`password="first\" still secret"`,
		`password='first\' still secret'`,
		`api_key="first\\\" still secret"`,
	} {
		inner := &sanitiserCapture{}
		h := NewSanitisingHandler(inner, s)
		r := slog.NewRecord(time.Time{}, slog.LevelInfo, "before "+value+" after", 0)
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		if inner.record.Message != "before "+Redaction+" after" {
			t.Errorf("credential leaked: %q", inner.record.Message)
		}
	}
}
