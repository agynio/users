package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

const (
	usernameMaxLength         = 32
	usernameSearchDefault     = 10
	usernameSearchMax         = 20
	usernameNumericSuffixMax  = 99
	usernameRandomSuffixLen   = 4
	usernameRandomSuffixTries = 5
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

func validateUsername(value string) error {
	if value == "" {
		return errors.New("value is required")
	}
	if len(value) > usernameMaxLength {
		return fmt.Errorf("max length %d", usernameMaxLength)
	}
	if !usernamePattern.MatchString(value) {
		return fmt.Errorf("must match %s", usernamePattern.String())
	}
	return nil
}

func validateUsernamePrefix(value string) error {
	if value == "" {
		return errors.New("value is required")
	}
	if len(value) > usernameMaxLength {
		return fmt.Errorf("max length %d", usernameMaxLength)
	}
	if !usernamePattern.MatchString(value) {
		return fmt.Errorf("must match %s", usernamePattern.String())
	}
	return nil
}

func normalizeUsernameCandidate(value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	var builder strings.Builder
	builder.Grow(len(lower))
	lastDash := false
	for _, r := range lower {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !isAllowed {
			if lastDash {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
			continue
		}
		if r == '-' {
			if lastDash {
				continue
			}
			lastDash = true
			builder.WriteRune(r)
			continue
		}
		lastDash = false
		builder.WriteRune(r)
	}
	normalized := strings.Trim(builder.String(), "-")
	if len(normalized) > usernameMaxLength {
		normalized = normalized[:usernameMaxLength]
	}
	return normalized
}

func deriveUsernameBase(preferredUsername, email, name, oidcSubject string) string {
	for _, candidate := range []string{preferredUsername, emailLocalPart(email), name} {
		normalized := normalizeUsernameCandidate(candidate)
		if normalized != "" {
			return normalized
		}
	}
	return fallbackUsername(oidcSubject)
}

func fallbackUsername(oidcSubject string) string {
	hash := sha256.Sum256([]byte(oidcSubject))
	return fmt.Sprintf("user-%s", hex.EncodeToString(hash[:])[:8])
}

func emailLocalPart(email string) string {
	if email == "" {
		return ""
	}
	parts := strings.SplitN(email, "@", 2)
	return parts[0]
}

type usernameCandidateIterator struct {
	base            string
	nextIndex       int
	randomGenerated int
	randomSuffix    func(int) (string, error)
}

func newUsernameCandidateIterator(base string) *usernameCandidateIterator {
	return &usernameCandidateIterator{
		base:         base,
		randomSuffix: randomUsernameSuffix,
	}
}

func (iter *usernameCandidateIterator) Next() (string, bool, error) {
	if iter.nextIndex == 0 {
		iter.nextIndex++
		return iter.base, true, nil
	}
	if iter.nextIndex < usernameNumericSuffixMax {
		suffix := fmt.Sprintf("-%d", iter.nextIndex+1)
		iter.nextIndex++
		return usernameWithSuffix(iter.base, suffix), true, nil
	}
	if iter.randomGenerated < usernameRandomSuffixTries {
		suffix, err := iter.randomSuffix(usernameRandomSuffixLen)
		if err != nil {
			return "", false, err
		}
		iter.randomGenerated++
		return usernameWithSuffix(iter.base, "-"+suffix), true, nil
	}
	return "", false, nil
}

func usernameWithSuffix(base, suffix string) string {
	maxBaseLength := usernameMaxLength - len(suffix)
	trimmed := base
	if len(trimmed) > maxBaseLength {
		trimmed = trimmed[:maxBaseLength]
		trimmed = strings.TrimRight(trimmed, "-")
	}
	return trimmed + suffix
}

func randomUsernameSuffix(length int) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	max := big.NewInt(int64(len(letters)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = letters[n.Int64()]
	}
	return string(b), nil
}
