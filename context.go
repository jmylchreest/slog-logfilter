package logfilter

import (
	"context"
	"sync"
)

// ContextExtractor is a function that extracts a string value from context.
// It should return the value and true if found, or empty string and false if not.
type ContextExtractor func(ctx context.Context) (string, bool)

// contextExtractors holds registered context extractors by key.
var (
	contextExtractors     = make(map[string]ContextExtractor)
	contextExtractorsLock sync.RWMutex
)

// RegisterContextExtractor registers a function to extract a value from context
// for the given key. This is used by filters with type "context:key".
//
// Example:
//
//	logfilter.RegisterContextExtractor("job_id", func(ctx context.Context) (string, bool) {
//	    if v := ctx.Value(JobIDKey); v != nil {
//	        if s, ok := v.(string); ok {
//	            return s, true
//	        }
//	    }
//	    return "", false
//	})
func RegisterContextExtractor(key string, extractor ContextExtractor) {
	contextExtractorsLock.Lock()
	defer contextExtractorsLock.Unlock()
	contextExtractors[key] = extractor
}

// UnregisterContextExtractor removes a context extractor for the given key.
func UnregisterContextExtractor(key string) {
	contextExtractorsLock.Lock()
	defer contextExtractorsLock.Unlock()
	delete(contextExtractors, key)
}

// GetContextExtractor returns the extractor for the given key, or nil if not registered.
func GetContextExtractor(key string) ContextExtractor {
	contextExtractorsLock.RLock()
	defer contextExtractorsLock.RUnlock()
	return contextExtractors[key]
}

// extractFromContext tries to extract a value from context using registered extractors.
func extractFromContext(ctx context.Context, key string) (string, bool) {
	if ctx == nil {
		return "", false
	}

	extractor := GetContextExtractor(key)
	if extractor == nil {
		return "", false
	}

	return extractor(ctx)
}

// ClearContextExtractors removes all registered context extractors.
// Useful for testing.
func ClearContextExtractors() {
	contextExtractorsLock.Lock()
	defer contextExtractorsLock.Unlock()
	contextExtractors = make(map[string]ContextExtractor)
}

// ContextExtractorKeys returns the keys of all registered context extractors.
func ContextExtractorKeys() []string {
	contextExtractorsLock.RLock()
	defer contextExtractorsLock.RUnlock()

	keys := make([]string, 0, len(contextExtractors))
	for k := range contextExtractors {
		keys = append(keys, k)
	}
	return keys
}
