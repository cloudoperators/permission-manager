package naming

import (
	"strings"
	"testing"
)

func TestTruncateWithHash(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLen    int
		want      string
		exactWant bool // if true, compare got == want exactly
	}{
		{
			name:      "short name unchanged",
			input:     "asr-my-short-name",
			maxLen:    253,
			want:      "asr-my-short-name",
			exactWant: true,
		},
		{
			name:      "empty string unchanged",
			input:     "",
			maxLen:    253,
			want:      "",
			exactWant: true,
		},
		{
			name:      "exactly at limit unchanged",
			input:     strings.Repeat("a", 253),
			maxLen:    253,
			want:      strings.Repeat("a", 253),
			exactWant: true,
		},
		{
			name:   "one char over limit triggers truncation",
			input:  strings.Repeat("a", 254),
			maxLen: 253,
		},
		{
			name:   "trailing hyphens trimmed before hash",
			input:  strings.Repeat("b", 244) + "----------" + strings.Repeat("c", 10),
			maxLen: 253,
		},
		{
			name:   "custom max length",
			input:  strings.Repeat("x", 100),
			maxLen: 50,
		},
		{
			name:   "long name with prefix",
			input:  "asr-" + strings.Repeat("a", 260),
			maxLen: 253,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateWithHash(tt.input, tt.maxLen)

			// If exact expected output is provided, check it
			if tt.exactWant {
				if got != tt.want {
					t.Errorf("TruncateWithHash(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
				}
				return
			}

			// Property-based checks for truncated results
			if len(got) > tt.maxLen {
				t.Errorf("result length %d exceeds maxLen %d", len(got), tt.maxLen)
			}

			// Must end with hex hash (dash + 8 hex chars)
			if len(got) < 9 {
				t.Fatalf("truncated result too short: %q", got)
			}
			suffix := got[len(got)-9:]
			if suffix[0] != '-' {
				t.Errorf("hash suffix should start with '-', got %q", suffix)
			}
			for _, c := range suffix[1:] {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
					t.Errorf("hash suffix contains non-hex char %q in %q", string(c), suffix)
				}
			}

			// The character before the hash suffix should NOT be a hyphen (trailing hyphen trim)
			prefix := got[:len(got)-9]
			if len(prefix) > 0 && prefix[len(prefix)-1] == '-' {
				t.Errorf("truncated prefix should not end with hyphen, got %q", got)
			}
		})
	}
}

func TestTruncateWithHash_Uniqueness(t *testing.T) {
	// Different long names must produce different truncated results
	name1 := strings.Repeat("a", 260) + "-org1"
	name2 := strings.Repeat("a", 260) + "-org2"

	got1 := TruncateWithHash(name1, 253)
	got2 := TruncateWithHash(name2, 253)

	if got1 == got2 {
		t.Errorf("different inputs should produce different results:\n  input1 suffix: -org1\n  input2 suffix: -org2\n  got: %q", got1)
	}
}

func TestTruncateWithHash_Deterministic(t *testing.T) {
	input := strings.Repeat("x", 300)
	first := TruncateWithHash(input, 253)

	for i := 0; i < 10; i++ {
		got := TruncateWithHash(input, 253)
		if got != first {
			t.Errorf("non-deterministic: call %d produced %q, expected %q", i, got, first)
		}
	}
}

func TestTruncateWithHash_K8sMaxDefault(t *testing.T) {
	// Ensure the function works correctly for the standard K8s max of 253
	longName := "asr-" + strings.Repeat("very-long-name-segment-", 20)
	got := TruncateWithHash(longName, MaxK8sNameLength)

	if len(got) > MaxK8sNameLength {
		t.Errorf("result exceeds K8s max: %d > %d", len(got), MaxK8sNameLength)
	}
}
