package logfilter

import (
	"context"
	"testing"
)

func TestContextExtractor_RegisterAndGet(t *testing.T) {
	defer ClearContextExtractors()

	// Initially no extractors
	if ext := GetContextExtractor("test_key"); ext != nil {
		t.Error("Expected nil extractor for unregistered key")
	}

	// Register an extractor
	RegisterContextExtractor("test_key", func(ctx context.Context) (string, bool) {
		return "test_value", true
	})

	// Should now be retrievable
	ext := GetContextExtractor("test_key")
	if ext == nil {
		t.Fatal("Expected extractor to be registered")
	}

	val, ok := ext(context.Background())
	if !ok || val != "test_value" {
		t.Errorf("Expected (test_value, true), got (%s, %v)", val, ok)
	}
}

func TestContextExtractor_Unregister(t *testing.T) {
	defer ClearContextExtractors()

	RegisterContextExtractor("test_key", func(ctx context.Context) (string, bool) {
		return "value", true
	})

	// Verify registered
	if GetContextExtractor("test_key") == nil {
		t.Fatal("Extractor should be registered")
	}

	// Unregister
	UnregisterContextExtractor("test_key")

	// Should be gone
	if GetContextExtractor("test_key") != nil {
		t.Error("Extractor should be unregistered")
	}
}

func TestContextExtractor_Keys(t *testing.T) {
	defer ClearContextExtractors()

	RegisterContextExtractor("key1", func(ctx context.Context) (string, bool) { return "", false })
	RegisterContextExtractor("key2", func(ctx context.Context) (string, bool) { return "", false })
	RegisterContextExtractor("key3", func(ctx context.Context) (string, bool) { return "", false })

	keys := ContextExtractorKeys()
	if len(keys) != 3 {
		t.Errorf("Expected 3 keys, got %d", len(keys))
	}

	// Check all keys are present (order not guaranteed)
	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	for _, expected := range []string{"key1", "key2", "key3"} {
		if !keyMap[expected] {
			t.Errorf("Missing key: %s", expected)
		}
	}
}

func TestContextExtractor_ExtractFromContext(t *testing.T) {
	defer ClearContextExtractors()

	type ctxKey string
	const myKey ctxKey = "my_value"

	RegisterContextExtractor("my_key", func(ctx context.Context) (string, bool) {
		if v := ctx.Value(myKey); v != nil {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
		return "", false
	})

	// With value in context
	ctx := context.WithValue(context.Background(), myKey, "hello")
	val, ok := extractFromContext(ctx, "my_key")
	if !ok || val != "hello" {
		t.Errorf("Expected (hello, true), got (%s, %v)", val, ok)
	}

	// Without value in context
	val, ok = extractFromContext(context.Background(), "my_key")
	if ok || val != "" {
		t.Errorf("Expected ('', false), got (%s, %v)", val, ok)
	}

	// With nil context
	val, ok = extractFromContext(nil, "my_key")
	if ok || val != "" {
		t.Errorf("Expected ('', false) for nil context, got (%s, %v)", val, ok)
	}

	// With unregistered key
	val, ok = extractFromContext(ctx, "unknown_key")
	if ok || val != "" {
		t.Errorf("Expected ('', false) for unknown key, got (%s, %v)", val, ok)
	}
}

func TestContextExtractor_Clear(t *testing.T) {
	RegisterContextExtractor("a", func(ctx context.Context) (string, bool) { return "", false })
	RegisterContextExtractor("b", func(ctx context.Context) (string, bool) { return "", false })

	if len(ContextExtractorKeys()) != 2 {
		t.Error("Expected 2 extractors before clear")
	}

	ClearContextExtractors()

	if len(ContextExtractorKeys()) != 0 {
		t.Error("Expected 0 extractors after clear")
	}
}
