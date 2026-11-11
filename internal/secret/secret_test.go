package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndKeyReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600", info.Mode().Perm())
	}
	enc, err := a.Encrypt([]byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := a.Encrypt([]byte("hunter2"))
	if enc == enc2 {
		t.Fatal("two encryptions of the same value must differ (random nonce)")
	}
	b, err := LoadOrCreate(path) // second load reads the same key
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Decrypt(enc)
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
}

func TestTamperAndWrongKey(t *testing.T) {
	a, _ := New(make([]byte, 32))
	enc, _ := a.Encrypt([]byte("x"))
	if _, err := a.Decrypt(enc[:len(enc)-2] + "AA"); err == nil {
		t.Fatal("tampered ciphertext must not decrypt")
	}
	key := make([]byte, 32)
	key[0] = 1
	b, _ := New(key)
	if _, err := b.Decrypt(enc); err == nil {
		t.Fatal("wrong key must not decrypt")
	}
	if _, err := a.Decrypt("plain"); err == nil {
		t.Fatal("unprefixed value must be rejected")
	}
}
