package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const (
	tokenPrefix      = "agyn_"
	tokenRandomChars = 44
)

var base62Alphabet = []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
var base62 = big.NewInt(62)

type GeneratedToken struct {
	Plaintext   string
	Hash        string
	TokenPrefix string
}

func Generate() (GeneratedToken, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return GeneratedToken{}, err
	}

	encoded := encodeBase62(bytes)
	if len(encoded) > tokenRandomChars {
		return GeneratedToken{}, fmt.Errorf("token length %d exceeds %d", len(encoded), tokenRandomChars)
	}
	if len(encoded) < tokenRandomChars {
		encoded = strings.Repeat("0", tokenRandomChars-len(encoded)) + encoded
	}

	plaintext := tokenPrefix + encoded
	return GeneratedToken{
		Plaintext:   plaintext,
		Hash:        Hash(plaintext),
		TokenPrefix: plaintext[:8],
	}, nil
}

func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func HasPrefix(token string) bool {
	return strings.HasPrefix(token, tokenPrefix)
}

func encodeBase62(input []byte) string {
	value := new(big.Int).SetBytes(input)
	if value.Sign() == 0 {
		return "0"
	}

	var encoded []byte
	mod := new(big.Int)
	for value.Sign() > 0 {
		value.DivMod(value, base62, mod)
		encoded = append(encoded, base62Alphabet[mod.Int64()])
	}

	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}
