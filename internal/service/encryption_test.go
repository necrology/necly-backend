package service

import (
	"encoding/base64"
	"testing"
)

func TestCredentialCipherRoundTripsAndUsesRandomNonce(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewCredentialCipher(key)
	if err != nil {
		t.Fatalf("NewCredentialCipher() error = %v", err)
	}
	first, err := cipher.Encrypt([]byte(`{"username":"alice","password":"secret"}`))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, _ := cipher.Encrypt([]byte(`{"username":"alice","password":"secret"}`))
	if string(first) == string(second) {
		t.Fatal("Encrypt() reused a nonce")
	}
	plain, err := cipher.Decrypt(first)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(plain) != `{"username":"alice","password":"secret"}` {
		t.Fatalf("Decrypt() = %s", plain)
	}
}

func TestCredentialCipherRejectsTampering(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, _ := NewCredentialCipher(key)
	sealed, _ := cipher.Encrypt([]byte("sensitive"))
	sealed[len(sealed)-1] ^= 0xff
	if _, err := cipher.Decrypt(sealed); err == nil {
		t.Fatal("Decrypt() accepted modified ciphertext")
	}
}

func TestCredentialCipherRequiresExactly256BitKey(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, err := NewCredentialCipher(short); err == nil {
		t.Fatal("NewCredentialCipher() accepted a non-256-bit key")
	}
}
