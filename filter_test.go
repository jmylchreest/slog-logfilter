package logfilter

import (
	"testing"
	"time"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		// Exact match
		{"exact match", "abc123", "abc123", true},
		{"exact no match", "abc123", "abc456", false},
		{"exact empty pattern", "", "abc", false},
		{"exact empty value", "abc", "", false},

		// Prefix match
		{"prefix match", "abc*", "abc123", true},
		{"prefix match exact", "abc*", "abc", true},
		{"prefix no match", "abc*", "xyz123", false},
		{"prefix empty after star", "*", "anything", true},

		// Suffix match
		{"suffix match", "*123", "abc123", true},
		{"suffix match exact", "*123", "123", true},
		{"suffix no match", "*123", "abc456", false},

		// Contains match
		{"contains match", "*abc*", "xxxabcyyy", true},
		{"contains at start", "*abc*", "abcyyy", true},
		{"contains at end", "*abc*", "xxxabc", true},
		{"contains exact", "*abc*", "abc", true},
		{"contains no match", "*abc*", "xxxyyy", false},
		{"contains empty middle", "**", "anything", true},

		// Edge cases
		{"single star", "*", "anything", true},
		{"double star", "**", "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPattern(tt.pattern, tt.value)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsExpired(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	tests := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{"nil expires", nil, false},
		{"past expires", &past, true},
		{"future expires", &future, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := LogFilter{ExpiresAt: tt.expiresAt}
			if got := f.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsActive(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)

	tests := []struct {
		name      string
		enabled   bool
		expiresAt *time.Time
		want      bool
	}{
		{"enabled no expiry", true, nil, true},
		{"disabled no expiry", false, nil, false},
		{"enabled future expiry", true, &future, true},
		{"enabled past expiry", true, &past, false},
		{"disabled past expiry", false, &past, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := LogFilter{Enabled: tt.enabled, ExpiresAt: tt.expiresAt}
			if got := f.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsContextFilter(t *testing.T) {
	tests := []struct {
		filterType string
		want       bool
	}{
		{"context:user_id", true},
		{"context:job_id", true},
		{"context:", true},
		{"job_id", false},
		{"user_id", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.IsContextFilter(); got != tt.want {
				t.Errorf("IsContextFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_ContextKey(t *testing.T) {
	tests := []struct {
		filterType string
		want       string
	}{
		{"context:user_id", "user_id"},
		{"context:job_id", "job_id"},
		{"context:", ""},
		{"job_id", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.ContextKey(); got != tt.want {
				t.Errorf("ContextKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogFilter_Matches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"job prefix", "job_*", "job_abc123", true},
		{"job prefix no match", "job_*", "task_abc123", false},
		{"user exact", "user_123", "user_123", true},
		{"user exact no match", "user_123", "user_456", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := LogFilter{Pattern: tt.pattern}
			if got := f.Matches(tt.value); got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsSourceFilter(t *testing.T) {
	tests := []struct {
		filterType string
		want       bool
	}{
		{SourceFilePrefix, true},
		{SourceFunctionPrefix, true},
		{"source:other", false},
		{"context:user_id", false},
		{"job_id", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.IsSourceFilter(); got != tt.want {
				t.Errorf("IsSourceFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsSourceFileFilter(t *testing.T) {
	tests := []struct {
		filterType string
		want       bool
	}{
		{SourceFilePrefix, true},
		{SourceFunctionPrefix, false},
		{"source:other", false},
		{"job_id", false},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.IsSourceFileFilter(); got != tt.want {
				t.Errorf("IsSourceFileFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_IsSourceFunctionFilter(t *testing.T) {
	tests := []struct {
		filterType string
		want       bool
	}{
		{SourceFunctionPrefix, true},
		{SourceFilePrefix, false},
		{"source:other", false},
		{"job_id", false},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.IsSourceFunctionFilter(); got != tt.want {
				t.Errorf("IsSourceFunctionFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogFilter_AttributeKey_WithSourceFilters(t *testing.T) {
	tests := []struct {
		filterType string
		want       string
	}{
		{"job_id", "job_id"},
		{"user_id", "user_id"},
		{"context:job_id", ""},
		{SourceFilePrefix, ""},
		{SourceFunctionPrefix, ""},
	}

	for _, tt := range tests {
		t.Run(tt.filterType, func(t *testing.T) {
			f := LogFilter{Type: tt.filterType}
			if got := f.AttributeKey(); got != tt.want {
				t.Errorf("AttributeKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
