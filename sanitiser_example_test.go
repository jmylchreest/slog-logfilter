package logfilter_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jmylchreest/slog-logfilter"
)

// exampleOutput omits timestamps so example output is reproducible.
func exampleOutput() slog.Handler {
	return slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
}

func ExampleNewSanitiser() {
	sanitiser, err := logfilter.NewSanitiser(logfilter.DefaultSanitiserRules()...)
	if err != nil {
		panic(err)
	}
	logger := slog.New(logfilter.NewSanitisingHandler(exampleOutput(), sanitiser))
	logger.Info("payment", "username", "alice", "reference", "4111111111111111", "count", 2)
	// Output:
	// level=INFO msg=payment username=[REDACTED] reference=[REDACTED] count=2
}

func ExampleSanitiserRule_replaceFunc() {
	sanitiser, err := logfilter.NewSanitiser(
		logfilter.SanitiserRule{
			Pattern: `\bkey_[a-z0-9]+\b`,
			ReplaceFunc: func(match string) string {
				// Apply application-specific validation to each regex match.
				if match == "key_public" {
					return match
				}
				return logfilter.Redaction
			},
		},
		logfilter.SanitiserRule{
			KeyPattern: `^email$`,
			ReplaceFunc: func(value string) string {
				// Without Pattern, the callback receives the entire string.
				if _, domain, ok := strings.Cut(value, "@"); ok {
					return "[PRIVATE]@" + domain
				}
				return logfilter.Redaction
			},
		},
	)
	if err != nil {
		panic(err)
	}
	logger := slog.New(logfilter.NewSanitisingHandler(exampleOutput(), sanitiser))
	logger.Info("key_private key_public", "email", "alice@example.com")
	// Output:
	// level=INFO msg="[REDACTED] key_public" email=[PRIVATE]@example.com
}

func ExampleWithSanitisers() {
	credentials, err := logfilter.NewSanitiser(logfilter.SanitiserRule{
		KeyPattern: `^password$`, Replacement: "[SECRET]",
	})
	if err != nil {
		panic(err)
	}
	identities, err := logfilter.NewSanitiser(logfilter.SanitiserRule{
		KeyPattern: `^username$`, Replacement: "[PERSON]",
	})
	if err != nil {
		panic(err)
	}
	var output bytes.Buffer
	logger := logfilter.New(
		logfilter.WithOutput(&output), logfilter.WithSource(false),
		logfilter.WithSanitisers(credentials, identities),
	)
	logger.With("username", "alice").Info("login", "password", "secret")
	var record struct{ Username, Password string }
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		panic(err)
	}
	fmt.Println(record.Username, record.Password)
	// Output:
	// [PERSON] [SECRET]
}

func ExampleNewSanitiserWithConfig() {
	s, err := logfilter.NewSanitiserWithConfig(logfilter.SanitiserConfig{
		Rules:           logfilter.DefaultSanitiserRules(),
		SanitiseObjects: true,
	})
	if err != nil {
		panic(err)
	}
	input := struct {
		Password string
		Count    int
	}{"secret", 2}
	clean := s.SanitiseAttr(nil, slog.Any("payload", input))
	encoded, err := json.Marshal(clean.Value.Any())
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))
	fmt.Println(input.Password)
	// Output:
	// {"Count":2,"Password":"[REDACTED]"}
	// secret
}

func ExampleSanitiseError() {
	s, err := logfilter.NewSanitiser(logfilter.DefaultSanitiserRules()...)
	if err != nil {
		panic(err)
	}
	original := errors.New("request failed: api_key=secret")
	clean := logfilter.SanitiseError(original, s)
	fmt.Println(clean)
	fmt.Println(errors.Is(clean, original))
	// Output:
	// request failed: [REDACTED]
	// true
}
