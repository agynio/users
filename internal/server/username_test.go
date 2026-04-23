package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeUsernameCandidate(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "spaces and case",
			input:    "Jane Doe",
			expected: "jane-doe",
		},
		{
			name:     "invalid characters collapse",
			input:    "--Hello__World!!",
			expected: "hello__world",
		},
		{
			name:     "length limit",
			input:    strings.Repeat("A", usernameMaxLength+5),
			expected: strings.Repeat("a", usernameMaxLength),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := normalizeUsernameCandidate(testCase.input)
			if result != testCase.expected {
				t.Fatalf("expected %q, got %q", testCase.expected, result)
			}
		})
	}
}

func TestDeriveUsernameBase(t *testing.T) {
	base := deriveUsernameBase("Preferred_Name", "jane@example.com", "Jane", "subject")
	if base != "preferred_name" {
		t.Fatalf("expected preferred_name, got %q", base)
	}

	base = deriveUsernameBase("", "Jane.Doe+Team@example.com", "Jane", "subject")
	if base != "jane-doe-team" {
		t.Fatalf("expected jane-doe-team, got %q", base)
	}

	base = deriveUsernameBase("", "", "Ada Lovelace", "subject")
	if base != "ada-lovelace" {
		t.Fatalf("expected ada-lovelace, got %q", base)
	}

	hash := sha256.Sum256([]byte("subject"))
	expectedFallback := fmt.Sprintf("user-%s", hex.EncodeToString(hash[:])[:8])
	base = deriveUsernameBase("", "", "", "subject")
	if base != expectedFallback {
		t.Fatalf("expected fallback %q, got %q", expectedFallback, base)
	}
}

func TestUsernameCandidateIterator(t *testing.T) {
	randomCalls := 0
	iterator := &usernameCandidateIterator{
		base: "alice",
		randomSuffix: func(length int) (string, error) {
			randomCalls++
			return "r4nd", nil
		},
	}

	candidate, ok, err := iterator.Next()
	if err != nil || !ok {
		t.Fatalf("expected initial candidate, err=%v ok=%v", err, ok)
	}
	if candidate != "alice" {
		t.Fatalf("expected alice, got %q", candidate)
	}

	candidate, ok, err = iterator.Next()
	if err != nil || !ok {
		t.Fatalf("expected second candidate, err=%v ok=%v", err, ok)
	}
	if candidate != "alice-2" {
		t.Fatalf("expected alice-2, got %q", candidate)
	}

	for i := 3; i <= usernameNumericSuffixMax; i++ {
		candidate, ok, err = iterator.Next()
		if err != nil || !ok {
			t.Fatalf("expected numeric candidate %d, err=%v ok=%v", i, err, ok)
		}
	}

	if candidate != fmt.Sprintf("alice-%d", usernameNumericSuffixMax) {
		t.Fatalf("expected last numeric candidate, got %q", candidate)
	}
	if randomCalls != 0 {
		t.Fatalf("expected no random calls before numeric exhausted, got %d", randomCalls)
	}

	candidate, ok, err = iterator.Next()
	if err != nil || !ok {
		t.Fatalf("expected random candidate, err=%v ok=%v", err, ok)
	}
	if candidate != "alice-r4nd" {
		t.Fatalf("expected alice-r4nd, got %q", candidate)
	}
	if randomCalls != 1 {
		t.Fatalf("expected 1 random call, got %d", randomCalls)
	}
}

func TestUsernameWithSuffixTrims(t *testing.T) {
	base := strings.Repeat("a", usernameMaxLength)
	result := usernameWithSuffix(base, "-2")
	if len(result) != usernameMaxLength {
		t.Fatalf("expected length %d, got %d", usernameMaxLength, len(result))
	}
	if !strings.HasSuffix(result, "-2") {
		t.Fatalf("expected suffix -2, got %q", result)
	}
}
