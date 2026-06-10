package ccrn

import "testing"

func TestIsWildcard(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{name: "bare wildcard", input: "*", expect: true},
		{name: "prefix glob", input: "nw-a-*", expect: true},
		{name: "wildcard in middle", input: "foo*bar", expect: true},
		{name: "exact name no wildcard", input: "prod-eu", expect: false},
		{name: "empty string", input: "", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWildcard(tt.input)
			if got != tt.expect {
				t.Errorf("IsWildcard(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		input   string
		expect  bool
	}{
		// Exact match
		{name: "exact match", pattern: "prod-eu", input: "prod-eu", expect: true},
		{name: "exact no match", pattern: "prod-eu", input: "staging-us", expect: false},
		// Bare wildcard
		{name: "bare wildcard matches anything", pattern: "*", input: "prod-eu", expect: true},
		{name: "bare wildcard matches empty", pattern: "*", input: "", expect: true},
		// Prefix glob
		{name: "prefix glob matches", pattern: "nw-a-*", input: "nw-a-prod", expect: true},
		{name: "prefix glob matches exact prefix", pattern: "nw-a-*", input: "nw-a-", expect: true},
		{name: "prefix glob no match", pattern: "nw-a-*", input: "nw-b-prod", expect: false},
		{name: "prefix glob no match shorter", pattern: "nw-a-*", input: "nw-a", expect: false},
		{name: "prefix glob longer match", pattern: "prod-*", input: "prod-eu-west-1", expect: true},
		// Edge cases
		{name: "empty pattern no wildcard", pattern: "", input: "", expect: true},
		{name: "empty pattern vs non-empty", pattern: "", input: "foo", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchGlob(tt.pattern, tt.input)
			if got != tt.expect {
				t.Errorf("MatchGlob(%q, %q) = %v, want %v", tt.pattern, tt.input, got, tt.expect)
			}
		})
	}
}
