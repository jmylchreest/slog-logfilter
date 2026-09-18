package logfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// Sanitizer transforms emitted messages and attributes before formatting.
// Implementations must be safe for concurrent use and must not mutate inputs.
type Sanitizer interface {
	SanitizeText(string) string
	SanitizeAttr(groups []string, attr slog.Attr) slog.Attr
}

// WithSanitizer enables sanitization after filtering and before formatting.
// A nil sanitizer preserves the existing behavior.
func WithSanitizer(s Sanitizer) Option { return func(o *options) { o.sanitizer = s } }

// SanitizingHandler applies s to records and attributes, including WithAttrs.
// It can also wrap a custom output handler. Filtering belongs outside it.
func SanitizingHandler(next slog.Handler, s Sanitizer) slog.Handler {
	if s == nil {
		return next
	}
	return &sanitizingHandler{next: next, s: s}
}

type sanitizingHandler struct {
	next   slog.Handler
	s      Sanitizer
	groups []string
}

func (h *sanitizingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}
func (h *sanitizingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, h.s.SanitizeText(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool { clean.AddAttrs(h.s.SanitizeAttr(h.groups, a)); return true })
	return h.next.Handle(ctx, clean)
}
func (h *sanitizingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.s.SanitizeAttr(h.groups, a)
	}
	return &sanitizingHandler{next: h.next.WithAttrs(clean), s: h.s, groups: h.groups}
}
func (h *sanitizingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(append([]string(nil), h.groups...), name)
	return &sanitizingHandler{next: h.next.WithGroup(h.s.SanitizeText(name)), s: h.s, groups: groups}
}

// Redacted is the replacement for sensitive values.
const Redacted = "[redacted]"

// RedactorOptions extends the built-in credential rules. Patterns replace
// their entire match; callers should match only the secret-bearing portion.
// Configure once before use. The redactor retains no runtime secret registry.
type RedactorOptions struct {
	SensitiveKeys []string
	Patterns      []*regexp.Regexp
}

// Redactor sanitizes common credential fields, URL credentials and text.
// It cannot identify arbitrary secrets in otherwise unstructured prose.
type Redactor struct {
	keys     map[string]bool
	patterns []*regexp.Regexp
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
var defaultPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?i)\b(?:token|secret|password|passwd|client[_-]?secret|api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|authorization|cookie|set-cookie)\b["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s&,;]+)`),
}

// NewRedactor returns an immutable redactor with defaults plus opts' rules.
func NewRedactor(opts RedactorOptions) *Redactor {
	r := &Redactor{keys: make(map[string]bool), patterns: append([]*regexp.Regexp(nil), defaultPatterns...)}
	for _, k := range append([]string{"authorization", "proxyauthorization", "cookie", "setcookie", "password", "passwd", "secret", "clientsecret", "privatekey", "apikey", "token", "accesstoken", "refreshtoken", "idtoken", "credentials"}, opts.SensitiveKeys...) {
		r.keys[normalKey(k)] = true
	}
	for _, p := range opts.Patterns {
		if p != nil {
			r.patterns = append(r.patterns, p.Copy())
		}
	}
	return r
}
func normalKey(s string) string {
	return strings.Map(func(c rune) rune {
		switch c {
		case '_', '-', ' ':
			return -1
		}
		return c
	}, strings.ToLower(s))
}

// SanitizeText replaces recognizable credentials without retaining the input.
func (r *Redactor) SanitizeText(s string) string {
	if strings.Contains(s, "://") {
		s = urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
			u, err := url.Parse(raw)
			if err != nil {
				return Redacted
			}
			changed := false
			if u.User != nil {
				u.User = url.User(Redacted)
				changed = true
			}
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				return Redacted
			}
			for k := range q {
				if r.keys[normalKey(k)] {
					q.Set(k, Redacted)
					changed = true
				}
			}
			if !changed {
				return raw
			}
			u.RawQuery = q.Encode()
			return u.String()
		})
	}
	for _, p := range r.patterns {
		if p.MatchString(s) {
			s = p.ReplaceAllString(s, Redacted)
		}
	}
	return s
}

// SanitizeAttr handles groups, deferred values and JSON-shaped objects. Opaque
// values that cannot be safely encoded (including cycles) are replaced, never
// passed through to a formatter that could reveal unsanitized fields.
func (r *Redactor) SanitizeAttr(groups []string, a slog.Attr) slog.Attr {
	key := a.Key
	a.Key = r.SanitizeText(key)
	if r.keys[normalKey(key)] {
		a.Value = slog.StringValue(Redacted)
		return a
	}
	for _, g := range groups {
		if r.keys[normalKey(g)] {
			a.Value = slog.StringValue(Redacted)
			return a
		}
	}
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(r.SanitizeText(a.Value.String()))
	case slog.KindGroup:
		in := a.Value.Group()
		out := make([]slog.Attr, len(in))
		for i, v := range in {
			out[i] = r.SanitizeAttr(groups, v)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		a.Value = slog.AnyValue(r.safeAny(a.Value.Any()))
	}
	return a
}
func (r *Redactor) safeAny(v any) (out any) {
	// User-defined MarshalJSON/Error methods must not turn a logging failure
	// into an unsanitized panic report.
	defer func() {
		if recover() != nil {
			out = "[unloggable]"
		}
	}()
	switch v := v.(type) {
	case nil:
		return nil
	case error:
		return r.SanitizeText(v.Error())
	case []byte:
		return r.SanitizeText(string(v))
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "[unloggable]"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return "[unloggable]"
	}
	return r.cleanTree(tree)
}
func (r *Redactor) cleanTree(v any) any {
	switch v := v.(type) {
	case string:
		return r.SanitizeText(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			key := r.SanitizeText(k)
			if r.keys[normalKey(k)] {
				out[key] = Redacted
			} else {
				out[key] = r.cleanTree(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = r.cleanTree(x)
		}
		return out
	default:
		return v
	}
}

// SanitizeError protects the displayed error while retaining errors.Is/As
// behavior. Unwrapping deliberately exposes the original to trusted code.
func SanitizeError(err error, s Sanitizer) error {
	if err == nil || s == nil {
		return err
	}
	return &sanitizedError{err: err, s: s}
}

type sanitizedError struct {
	err error
	s   Sanitizer
}

func (e *sanitizedError) Error() string { return e.s.SanitizeText(e.err.Error()) }
func (e *sanitizedError) Unwrap() error { return e.err }
func (e *sanitizedError) Format(f fmt.State, verb rune) {
	text := e.Error()
	if verb == 'q' {
		_, _ = fmt.Fprintf(f, "%q", text)
	} else {
		_, _ = fmt.Fprint(f, text)
	}
}
