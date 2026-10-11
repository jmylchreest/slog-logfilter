package logfilter

import (
	"bytes"
	"encoding/json"
)

// decodeObject isolates panics from user-defined Error and MarshalJSON methods.
// Rule callbacks run after this returns, so their panics retain normal semantics.
func decodeObject(v any) (out any) {
	defer func() {
		if recover() != nil {
			out = "[unloggable]"
		}
	}()
	switch v := v.(type) {
	case nil:
		return nil
	case error:
		return v.Error()
	case []byte:
		return string(v)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "[unloggable]"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return "[unloggable]"
	}
	return out
}

func (s *Sanitiser) safeAny(v any, key string) any {
	return s.cleanTree(decodeObject(v), key)
}

// cleanTree receives newly decoded storage and may transform it in place.
func (s *Sanitiser) cleanTree(v any, key string) any {
	switch v := v.(type) {
	case string:
		for _, rule := range s.rules {
			if (rule.value != nil || rule.replaceFunc != nil) && (rule.key == nil || rule.key.MatchString(key)) {
				v = rule.text(v)
			}
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			outputKey := k
			if s.sanitiseKeys {
				outputKey = s.SanitiseText(k)
			}
			if replacement, ok := s.fieldReplacement(k); ok {
				out[outputKey] = replacement
			} else {
				out[outputKey] = s.cleanTree(x, k)
			}
		}
		return out
	case []any:
		for i, x := range v {
			v[i] = s.cleanTree(x, key)
		}
		return v
	default:
		return v
	}
}
