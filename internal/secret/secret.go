// Package secret encrypts values at rest with AES-256-GCM using a key file in the data directory.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	keySize = 32
	prefix  = "v1:"
)

type Box struct{ aead cipher.AEAD }

// LoadOrCreate reads the key at path, creating it (mode 0600) on first run.
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create key file: %w", err)
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return New(key)
}

// New builds a Box from a raw 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secret key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns "v1:" + base64(nonce || ciphertext).
func (b *Box) Encrypt(plain []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return prefix + base64.RawStdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (b *Box) Decrypt(s string) ([]byte, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return nil, errors.New("secret: unknown format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return nil, errors.New("secret: ciphertext too short")
	}
	return b.aead.Open(nil, raw[:n], raw[n:], nil)
}
