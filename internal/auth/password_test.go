package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected PHC prefix: %q", hash)
	}

	ok, rehash, err := VerifyPassword(hash, []byte("correct horse battery staple"))
	if err != nil || !ok || rehash {
		t.Fatalf("verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	ok, _, err = VerifyPassword(hash, []byte("wrong password"))
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}

	// Distinct salts: two hashes of the same password differ.
	hash2, _ := HashPassword([]byte("correct horse battery staple"))
	if hash == hash2 {
		t.Fatal("salts not random")
	}
}

func TestPasswordPHCParsing(t *testing.T) {
	cases := []string{
		"",                       // empty
		"$argon2i$v=19$m=1,t=1,p=1$AAAA$AAAA",   // wrong variant
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$AAAA", // bad base64 salt
		"$argon2id$v=19$m=19456,t=2$AAAA$AAAA",  // missing param
		"not-a-phc-string",
	}
	for _, c := range cases {
		if ok, _, err := VerifyPassword(c, []byte("x")); ok || err == nil {
			t.Fatalf("malformed hash %q: ok=%v err=%v (want !ok, err)", c, ok, err)
		}
	}
}

func TestPasswordNeedsRehash(t *testing.T) {
	// A hash minted at weaker-than-current parameters verifies but flags rehash.
	old := mustHashWithParams(t, []byte("pw"), 1, 8*1024, 1)
	ok, rehash, err := VerifyPassword(old, []byte("pw"))
	if err != nil || !ok || !rehash {
		t.Fatalf("old-params verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

// mustHashWithParams mints a PHC hash at explicit parameters (test-only).
func mustHashWithParams(t *testing.T, pw []byte, time_, memory uint32, threads uint8) string {
	t.Helper()
	h, err := hashWithParams(pw, time_, memory, threads)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
