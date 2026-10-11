package logfilter

import "context"

// ContextExtractor extracts a string from a context and reports whether it exists.
// Extractors may be called concurrently and must be safe for concurrent use.
type ContextExtractor func(ctx context.Context) (string, bool)

// RegisterContextExtractor sets an extractor for this handler and all its derived
// handlers. A nil extractor behaves as an absent value. Callbacks run without
// holding the registry lock, so they may safely update configuration themselves.
func (h *Handler) RegisterContextExtractor(key string, extractor ContextExtractor) {
	h.extractorsLock.Lock()
	defer h.extractorsLock.Unlock()
	if h.extractors == nil {
		h.extractors = make(map[string]ContextExtractor)
	}
	h.extractors[key] = extractor
}

// UnregisterContextExtractor removes an extractor from this logger family.
func (h *Handler) UnregisterContextExtractor(key string) {
	h.extractorsLock.Lock()
	defer h.extractorsLock.Unlock()
	delete(h.extractors, key)
}

// GetContextExtractor returns an extractor, or nil if it is not registered.
func (h *Handler) GetContextExtractor(key string) ContextExtractor {
	h.extractorsLock.RLock()
	defer h.extractorsLock.RUnlock()
	return h.extractors[key]
}

// ClearContextExtractors clears the extractor registry for this logger family.
func (h *Handler) ClearContextExtractors() {
	h.extractorsLock.Lock()
	defer h.extractorsLock.Unlock()
	h.extractors = nil
}

// ContextExtractorKeys returns the extractor keys for this logger family.
// The order is unspecified.
func (h *Handler) ContextExtractorKeys() []string {
	h.extractorsLock.RLock()
	defer h.extractorsLock.RUnlock()
	keys := make([]string, 0, len(h.extractors))
	for key := range h.extractors {
		keys = append(keys, key)
	}
	return keys
}

func (h *Handler) extractFromContext(ctx context.Context, key string) (string, bool) {
	if ctx == nil {
		return "", false
	}
	extractor := h.GetContextExtractor(key)
	if extractor == nil {
		return "", false
	}
	return extractor(ctx)
}
