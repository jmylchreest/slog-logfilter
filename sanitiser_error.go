package logfilter

import "fmt"

// SanitiseError sanitises displayed error text while preserving errors.Is and
// errors.As through Unwrap. Sanitisers run in order. Nil and inactive sanitisers
// are ignored; a nil error or no active sanitisers returns the original error.
// Unwrap intentionally exposes the original error to trusted code. Formatting
// uses the sanitised Error text, including for %+v and %#v; original formatter
// details are not retained. Rule callbacks must be safe for concurrent calls.
func SanitiseError(err error, sanitisers ...*Sanitiser) error {
	if err == nil {
		return nil
	}
	var active []*Sanitiser
	for _, s := range sanitisers {
		if s != nil && len(s.rules) != 0 {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return err
	}
	return &sanitisedError{err: err, sanitisers: active}
}

type sanitisedError struct {
	err        error
	sanitisers []*Sanitiser
}

func (e *sanitisedError) Error() string {
	text := e.err.Error()
	for _, s := range e.sanitisers {
		text = s.SanitiseText(text)
	}
	return text
}
func (e *sanitisedError) Unwrap() error { return e.err }
func (e *sanitisedError) Format(f fmt.State, verb rune) {
	text := e.Error()
	if verb == 'q' {
		_, _ = fmt.Fprintf(f, "%q", text)
	} else {
		_, _ = fmt.Fprint(f, text)
	}
}
