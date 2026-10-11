package logfilter

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
)

// Redaction is the default replacement for sensitive values.
const Redaction = "[REDACTED]"

// SanitiserRule redacts fields or transforms text using Go regular expressions
// and optional callbacks. Unlike [LogFilter] patterns, these are regular
// expressions, not glob patterns.
//
// A KeyPattern without Pattern or ReplaceFunc replaces the entire matching value,
// including groups and non-string values. Other rules process strings only.
// Without KeyPattern, text rules apply to messages and all string attributes.
//
// Whole-field replacement takes precedence over text rules; see [Sanitiser].
type SanitiserRule struct {
	// KeyPattern selects local attribute names at any group depth, not dotted paths.
	// Use ^ and $ for whole-key matching and (?i) for case-insensitive matching.
	KeyPattern string `json:"key_pattern,omitempty"`
	// Pattern selects substrings to replace. When ReplaceFunc is set, each complete
	// match is passed to it; capture groups are not passed separately.
	Pattern string `json:"pattern,omitempty"`
	// Replacement is literal (no $ expansion); empty means Redaction.
	Replacement string `json:"replacement,omitempty"`
	// ReplaceFunc computes a literal replacement for each Pattern match. Without
	// Pattern it receives the whole string. Return the input to leave it unchanged,
	// or an empty string to remove it. KeyPattern restricts it to matching string
	// attributes; otherwise it also processes messages. Non-string values are skipped.
	// It cannot be combined with Replacement and is not serialised to JSON.
	// Callbacks must be safe for concurrent use. Panics propagate to the caller.
	// Bound attributes are processed when WithAttrs is called, not on every log.
	// Reattach callbacks after loading JSON; a JSON round trip changes behaviour.
	ReplaceFunc func(string) string `json:"-"`
}

type sanitiserRule struct {
	key, value  *regexp.Regexp
	replacement string
	replaceFunc func(string) string
}

// Sanitiser configuration is immutable. Concurrent use requires concurrency-safe
// callbacks. Its zero value does nothing.
//
// Rules with only KeyPattern (no Pattern or ReplaceFunc) take precedence and
// replace the whole attribute value; the first match wins and stops processing
// that attribute. Otherwise text rules run in order on the preceding result.
// This ordering applies within each sanitiser. [WithSanitisers] applies separate
// sanitisers in the supplied order, including to earlier replacement text.
type Sanitiser struct{ rules []sanitiserRule }

// NewSanitiser validates and compiles rules once, returning no sanitiser on error.
// It rejects invalid regular expressions, rules without any matcher or callback,
// and rules combining ReplaceFunc with a non-empty Replacement. With no rules,
// it returns an inactive sanitiser. The supplied rules are copied; any state
// captured by a callback remains the caller's responsibility.
//
// No default rules are included automatically. Pass [DefaultSanitiserRules] to
// opt into them, then install the result with [WithSanitiser], [WithSanitisers],
// or [NewSanitisingHandler].
func NewSanitiser(rules ...SanitiserRule) (*Sanitiser, error) {
	s := &Sanitiser{}
	for i, rule := range rules {
		if rule.KeyPattern == "" && rule.Pattern == "" && rule.ReplaceFunc == nil {
			return nil, fmt.Errorf("sanitiser rule %d: key_pattern, pattern or replace function is required", i)
		}
		if rule.ReplaceFunc != nil && rule.Replacement != "" {
			return nil, fmt.Errorf("sanitiser rule %d: replacement and replace function are mutually exclusive", i)
		}
		r := sanitiserRule{replacement: rule.Replacement, replaceFunc: rule.ReplaceFunc}
		if r.replacement == "" {
			r.replacement = Redaction
		}
		var err error
		if rule.KeyPattern != "" {
			r.key, err = regexp.Compile(rule.KeyPattern)
			if err != nil {
				return nil, fmt.Errorf("sanitiser rule %d key_pattern: %w", i, err)
			}
		}
		if rule.Pattern != "" {
			r.value, err = regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, fmt.Errorf("sanitiser rule %d pattern: %w", i, err)
			}
		}
		s.rules = append(s.rules, r)
	}
	return s, nil
}

// DefaultSanitiserRules returns a fresh set of opt-in rules for UUIDs,
// credential fields, labelled credentials in text, and card-like numbers.
// Card matching uses 13–19 digits with optional spaces or hyphens, followed by
// Luhn checksum validation. It does not validate issuers and can redact other IDs
// with valid checksums. Explicit card fields are redacted regardless of validity.
// These rules cannot identify arbitrary unlabelled usernames or passwords.
// Phone numbers, email addresses, and provider-specific API key formats are not
// explicitly detected. API key fields named api_key or apiKey are redacted.
// The card rule includes a ReplaceFunc callback, which is lost if rules are
// serialised to JSON; call this function again to retain the built-in behaviour.
func DefaultSanitiserRules() []SanitiserRule {
	return []SanitiserRule{
		{KeyPattern: `(?i)^(user_?name|user_?id|password|passwd|pwd|secret|token|access_?token|refresh_?token|api_?key|authorization|credit_?card|card_?number|cvv|cvc)$`},
		{Pattern: `(?i)\bbearer\s+[a-z0-9._~+/-]+=*`},
		{Pattern: `(?i)\b(username|user_name|password|passwd|pwd|secret|token|access_token|refresh_token|api_key|authorization)\s*[:=]\s*("(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;]+)`},
		{Pattern: `(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`},
		{Pattern: `\b[0-9](?:[ -]?[0-9]){12,18}\b`, ReplaceFunc: redactCardNumber},
	}
}

// redactCardNumber validates candidates selected by the default card regex.
func redactCardNumber(candidate string) string {
	sum, digits := 0, 0
	double, nonzero := false, false
	for i := len(candidate) - 1; i >= 0; i-- {
		c := candidate[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			return candidate
		}
		n := int(c - '0')
		nonzero = nonzero || n != 0
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		digits++
		double = !double
	}
	if digits >= 13 && digits <= 19 && nonzero && sum%10 == 0 {
		return Redaction
	}
	return candidate
}

func (r sanitiserRule) text(value string) string {
	// The regexp replacement methods allocate even when no substring matches.
	// Most log strings are not sensitive; preserve them without rebuilding them.
	if r.value != nil && !r.value.MatchString(value) {
		return value
	}
	if r.replaceFunc != nil {
		if r.value == nil {
			return r.replaceFunc(value)
		}
		return r.value.ReplaceAllStringFunc(value, r.replaceFunc)
	}
	return r.value.ReplaceAllLiteralString(value, r.replacement)
}

// NewSanitisingHandler wraps inner with sanitisation of messages and attributes.
// Sanitisers run in the supplied order, each receiving the preceding output.
// Nil and empty sanitisers are ignored; no active sanitisers returns inner unchanged.
//
// Wrap this handler inside [NewHandler] to ensure level filters see original values. Attributes already
// bound to inner, context contents, and metadata added by inner are not scanned.
// Opaque Any values (maps, structs, errors, etc.) are not traversed: redact them
// by key or expose their fields as slog groups or LogValuer values.
// Nested groups are traversed and LogValuer values are resolved before text rules
// run. Whole-field replacement avoids resolving the value being redacted.
func NewSanitisingHandler(inner slog.Handler, sanitisers ...*Sanitiser) slog.Handler {
	// Wrap in reverse so the first configured sanitiser processes each input first,
	// including attributes bound via WithAttrs.
	for i := len(sanitisers) - 1; i >= 0; i-- {
		s := sanitisers[i]
		if s != nil && len(s.rules) != 0 {
			inner = &sanitisingHandler{inner: inner, sanitiser: s}
		}
	}
	return inner
}

type sanitisingHandler struct {
	inner     slog.Handler
	sanitiser *Sanitiser
}

func (h *sanitisingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *sanitisingHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := r.Message
	for _, rule := range h.sanitiser.rules {
		if rule.key == nil {
			msg = rule.text(msg)
		}
	}
	var attrs []slog.Attr
	index := 0
	r.Attrs(func(a slog.Attr) bool {
		clean, changed := h.sanitiser.attrChanged(a)
		if changed && attrs == nil {
			attrs = make([]slog.Attr, 0, r.NumAttrs())
			// Preserve the unchanged prefix without copying any attribute storage
			// until a modification is actually needed.
			r.Attrs(func(prefix slog.Attr) bool {
				if len(attrs) == index {
					return false
				}
				attrs = append(attrs, prefix)
				return true
			})
		}
		if attrs != nil {
			attrs = append(attrs, clean)
		}
		index++
		return true
	})
	r.Message = msg
	if attrs != nil {
		out := slog.NewRecord(r.Time, r.Level, msg, r.PC)
		out.AddAttrs(attrs...)
		r = out
	}
	return h.inner.Handle(ctx, r)
}

func (h *sanitisingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = h.sanitiser.attr(a)
	}
	return &sanitisingHandler{inner: h.inner.WithAttrs(out), sanitiser: h.sanitiser}
}

func (h *sanitisingHandler) WithGroup(name string) slog.Handler {
	return &sanitisingHandler{inner: h.inner.WithGroup(name), sanitiser: h.sanitiser}
}

func (s *Sanitiser) attr(a slog.Attr) slog.Attr {
	clean, _ := s.attrChanged(a)
	return clean
}

// attrChanged copies group storage only when a child needs modification.
func (s *Sanitiser) attrChanged(a slog.Attr) (slog.Attr, bool) {
	if a.Equal(slog.Attr{}) {
		return a, false
	}
	// Whole-field redaction avoids evaluating sensitive LogValuer values.
	for _, rule := range s.rules {
		if rule.value == nil && rule.replaceFunc == nil && rule.key.MatchString(a.Key) {
			return slog.String(a.Key, rule.replacement), true
		}
	}
	changed := a.Value.Kind() == slog.KindLogValuer
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindGroup:
		attrs := a.Value.Group()
		var out []slog.Attr
		for i, child := range attrs {
			clean, modified := s.attrChanged(child)
			if modified && out == nil {
				out = make([]slog.Attr, len(attrs))
				copy(out, attrs)
			}
			if out != nil {
				out[i] = clean
			}
		}
		if out != nil {
			a.Value = slog.GroupValue(out...)
			changed = true
		}
	case slog.KindString:
		value := a.Value.String()
		for _, rule := range s.rules {
			if (rule.value != nil || rule.replaceFunc != nil) && (rule.key == nil || rule.key.MatchString(a.Key)) {
				value = rule.text(value)
			}
		}
		if value != a.Value.String() {
			a.Value = slog.StringValue(value)
			changed = true
		}
	}
	return a, changed
}
