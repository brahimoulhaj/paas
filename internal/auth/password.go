// Package auth holds password hashing and opaque token helpers.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Hashes are stored as "pbkdf2-sha256$<iterations>$<salt>$<hash>". The algorithm id
// leads the string so another scheme (argon2id) can be added without a migration.
const (
	algo       = "pbkdf2-sha256"
	iterations = 600_000 // OWASP recommendation for PBKDF2-HMAC-SHA256
	saltLen    = 16
	hashLen    = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum, err := pbkdf2.Key(sha256.New, password, salt, iterations, hashLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", algo, iterations, enc.EncodeToString(salt), enc.EncodeToString(sum)), nil
}

// VerifyPassword reports whether password matches the stored hash.
func VerifyPassword(stored, password string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != algo {
		return false, errors.New("auth: unsupported password hash")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false, errors.New("auth: bad iteration count")
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false, errors.New("auth: bad password hash encoding")
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
