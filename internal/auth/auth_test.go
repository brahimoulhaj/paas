package auth

import "testing"

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(h, "correct horse"); err != nil || !ok {
		t.Fatalf("verify good = %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "wrong"); ok {
		t.Fatal("wrong password verified")
	}
	if _, err := VerifyPassword("garbage", "x"); err == nil {
		t.Fatal("malformed hash must error")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) != 43 {
		t.Fatalf("tokens: %q %q", a, b)
	}
	if HashToken(a) == HashToken(b) || len(HashToken(a)) != 64 {
		t.Fatal("bad token hash")
	}
}
