package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
)

func testAccount(t *testing.T) string {
	b := make([]byte, 6)
	rand.Read(b)
	return "encore-test-" + hex.EncodeToString(b)
}

// TestKeychain uses the real login keychain with a throwaway account.
func TestKeychain(t *testing.T) {
	if _, err := os.Stat(securityPath); err != nil {
		t.Skip("no security(1)")
	}
	// HOME stays real: security(1) finds the login keychain through it.
	acct := testAccount(t)
	t.Cleanup(func() { keychainDelete(acct) })

	if _, err := SecretGet(acct); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("missing secret: %v", err)
	}
	for _, v := range []string{`{"apiKey":"k","secret":"s\"q\\"}`, "plain", "héllo wörld", ""} {
		if err := SecretSet(acct, v); err != nil {
			t.Fatalf("set %q: %v", v, err)
		}
		if got, err := keychainGet(acct); err != nil || got != v {
			t.Fatalf("keychain round trip: got %q %v, want %q", got, err, v)
		}
		if got, err := SecretGet(acct); err != nil || got != v {
			t.Fatalf("SecretGet: got %q %v, want %q", got, err, v)
		}
	}
	if _, err := fileGet(acct); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("keychain path should not write the fallback file: %v", err)
	}
	if err := SecretDelete(acct); err != nil {
		t.Fatal(err)
	}
	if _, err := SecretGet(acct); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("after delete: %v", err)
	}
	if err := SecretDelete(acct); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestSecretFileFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := securityPath
	securityPath = "/nonexistent/security"
	t.Cleanup(func() { securityPath = old })

	acct := testAccount(t)
	if err := SecretSet(acct, "s3cret"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(secretsPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("fallback file: %v %v", fi, err)
	}
	if got, err := SecretGet(acct); err != nil || got != "s3cret" {
		t.Fatalf("get: %q %v", got, err)
	}
	if err := SecretDelete(acct); err != nil {
		t.Fatal(err)
	}
	if _, err := SecretGet(acct); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := os.Stat(secretsPath()); !os.IsNotExist(err) {
		t.Fatal("empty fallback file should be removed")
	}
}

func TestSecretAccountValidation(t *testing.T) {
	for _, a := range []string{"", "has space", "semi;colon", "-a", "x\ny"} {
		if err := SecretSet(a, "x"); err == nil {
			t.Errorf("account %q accepted", a)
		}
	}
}
