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
// including groups and non-string values. It also redacts attributes inside
// a matching WithGroup. Other rules process strings only (including strings
// decoded from objects when SanitiserConfig.SanitiseObjects is enabled).
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
type Sanitiser struct {
	rules           []sanitiserRule
	sanitiseObjects bool
	sanitiseKeys    bool
}

// SanitiserConfig controls rule evaluation and optional structural conversion.
// No default rules are added automatically.
type SanitiserConfig struct {
	Rules []SanitiserRule
	// SanitiseObjects converts opaque Any values through JSON before applying
	// rules recursively. Errors and byte slices become sanitised strings.
	// Failed or panicking encoders and cyclic objects become "[unloggable]".
	// This changes output types and adds allocations; it is disabled by default.
	SanitiseObjects bool
	// SanitiseKeys applies unrestricted text rules to attribute names and group
	// names too. KeyPattern selection always uses the original names.
	// This can change the output schema; it is disabled by default.
	SanitiseKeys bool
}

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
	return NewSanitiserWithConfig(SanitiserConfig{Rules: rules})
}

// NewSanitiserWithConfig constructs an immutable sanitiser with explicit
// structural options. Rules have the same validation and ordering as NewSanitiser.
func NewSanitiserWithConfig(config SanitiserConfig) (*Sanitiser, error) {
	s := &Sanitiser{sanitiseObjects: config.SanitiseObjects, sanitiseKeys: config.SanitiseKeys}
	rules := config.Rules
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
// credential fields, labelled credentials, Bearer/Basic authentication, HTTP(S)
// URL credentials, private-key blocks (including truncated blocks), and card-like numbers.
// Card matching uses 13–19 digits with optional spaces or hyphens, followed by
// Luhn checksum validation. It does not validate issuers and can redact other IDs
// with valid checksums. Explicit card fields are redacted regardless of validity.
// These rules cannot identify arbitrary unlabelled usernames or passwords.
// Phone numbers, email addresses, and provider-specific API key formats are not
// explicitly detected. API key fields named api_key or apiKey are redacted.
// URL parsing and card validation use ReplaceFunc callbacks, which are lost if
// rules are serialised to JSON; call this function again to retain the behaviour.
// URL query recognition uses the default credential names. Add a custom callback
// for application-specific query parameters.
func DefaultSanitiserRules() []SanitiserRule {
	return []SanitiserRule{
		{KeyPattern: "(?i)^(" + credentialNames + ")$"},
		// URLs must be parsed before assignment rules alter their query strings.
		{Pattern: `(?i)https?://[^\s<>"']+`, ReplaceFunc: redactURL},
		{Pattern: `(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`},
		{Pattern: `(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`},
		{Pattern: "(?i)\\b(" + credentialNames + `)\b["']?\s*[:=]\s*("(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s&,;]+)`},
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
// Wrap this handler inside [NewHandler] to ensure filters see original values.
// Attributes already bound to inner, context contents and metadata added by inner
// are not scanned. By default, opaque Any values are not traversed: redact them
// by key, expose slog groups, or explicitly enable SanitiserConfig.SanitiseObjects.
// Attribute/group names are scanned only with SanitiserConfig.SanitiseKeys.
// Whole-field rules matching an enclosing WithGroup name redact its attributes.
// Nested groups are traversed and LogValuer values are resolved before text rules
// run. Whole-field replacement avoids resolving the value being redacted.
func NewSanitisingHandler(inner slog.Handler, sanitisers ...*Sanitiser) slog.Handler {
	// Wrap in reverse so the first configured sanitiser processes each input first,
	// including attributes bound via WithAttrs.
	for i := len(sanitisers) - 1; i >= 0; i-- {
		s := sanitisers[i]
		if s != nil && (len(s.rules) != 0 || s.sanitiseObjects) {
			inner = &sanitisingHandler{inner: inner, sanitiser: s}
		}
	}
	return inner
}

type sanitisingHandler struct {
	inner     slog.Handler
	sanitiser *Sanitiser
	groups    []string
}

func (h *sanitisingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *sanitisingHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := h.sanitiser.SanitiseText(r.Message)
	var attrs []slog.Attr
	index := 0
	r.Attrs(func(a slog.Attr) bool {
		clean, changed := h.sanitiser.attrInGroups(h.groups, a)
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
		out[i] = h.sanitiser.SanitiseAttr(h.groups, a)
	}
	return &sanitisingHandler{inner: h.inner.WithAttrs(out), sanitiser: h.sanitiser, groups: h.groups}
}

func (h *sanitisingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(append([]string(nil), h.groups...), name)
	outputName := name
	if h.sanitiser.sanitiseKeys {
		outputName = h.sanitiser.SanitiseText(name)
	}
	return &sanitisingHandler{inner: h.inner.WithGroup(outputName), sanitiser: h.sanitiser, groups: groups}
}

// SanitiseText applies unrestricted text rules in order. Key-specific rules are
// ignored. A nil sanitiser leaves the input unchanged.
func (s *Sanitiser) SanitiseText(value string) string {
	if s == nil {
		return value
	}
	for _, rule := range s.rules {
		if rule.key == nil {
			value = rule.text(value)
		}
	}
	return value
}

// SanitiseAttr applies rules to an attribute without modifying its input.
// Groups contains the enclosing group names. A whole-field rule matching an
// enclosing name redacts the attribute before resolving its value.
// A nil sanitiser leaves the attribute unchanged.
func (s *Sanitiser) SanitiseAttr(groups []string, a slog.Attr) slog.Attr {
	if s == nil || (len(s.rules) == 0 && !s.sanitiseObjects) {
		return a
	}
	clean, _ := s.attrInGroups(groups, a)
	return clean
}

func (s *Sanitiser) fieldReplacement(key string) (string, bool) {
	for _, rule := range s.rules {
		if rule.value == nil && rule.replaceFunc == nil && rule.key.MatchString(key) {
			return rule.replacement, true
		}
	}
	return "", false
}

func (s *Sanitiser) attrInGroups(groups []string, a slog.Attr) (slog.Attr, bool) {
	if a.Equal(slog.Attr{}) {
		return a, false
	}
	for _, group := range groups {
		if replacement, ok := s.fieldReplacement(group); ok {
			key := a.Key
			if s.sanitiseKeys {
				key = s.SanitiseText(key)
			}
			return slog.String(key, replacement), true
		}
	}
	return s.attrChanged(a)
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
	key := a.Key
	if s.sanitiseKeys {
		a.Key = s.SanitiseText(key)
	}
	// Whole-field redaction avoids evaluating sensitive LogValuer values.
	if replacement, ok := s.fieldReplacement(key); ok {
		return slog.String(a.Key, replacement), true
	}
	changed := a.Key != key || a.Value.Kind() == slog.KindLogValuer
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
	case slog.KindAny:
		if s.sanitiseObjects && a.Value.Any() != nil {
			a.Value = slog.AnyValue(s.safeAny(a.Value.Any(), key))
			changed = true
		}
	case slog.KindString:
		value := a.Value.String()
		for _, rule := range s.rules {
			if (rule.value != nil || rule.replaceFunc != nil) && (rule.key == nil || rule.key.MatchString(key)) {
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
