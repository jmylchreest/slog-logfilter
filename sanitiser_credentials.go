package logfilter

import (
	"net/url"
	"regexp"
)

// Keep URL-query and field-name recognition consistent. The regexp is immutable.
const credentialNames = `user[_ -]?name|user[_ -]?id|password|passwd|pwd|secret|client[_ -]?secret|private[_ -]?key|token|access[_ -]?token|refresh[_ -]?token|id[_ -]?token|api[_ -]?key|authorization|proxy[_ -]?authorization|cookie|set[_ -]?cookie|credentials|credit[_ -]?card|card[_ -]?number|cvv|cvc`

var credentialKey = regexp.MustCompile("(?i)^(" + credentialNames + ")$")

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return Redaction
	}
	changed := false
	if u.User != nil {
		u.User = url.User(Redaction)
		changed = true
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Redaction
	}
	for key := range query {
		if credentialKey.MatchString(key) {
			query.Set(key, Redaction)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = query.Encode()
	return u.String()
}
