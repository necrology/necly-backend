package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const credentialVersion byte = 1

type CredentialCipher struct {
	aead cipher.AEAD
}

func NewCredentialCipher(base64Key string) (*CredentialCipher, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("decode credential key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("credential encryption key must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &CredentialCipher{aead: aead}, nil
}

func (c *CredentialCipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out := make([]byte, 1, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out[0] = credentialVersion
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plaintext, []byte("necly-inventory-credentials-v1")), nil
}

func (c *CredentialCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 1+c.aead.NonceSize()+c.aead.Overhead() || ciphertext[0] != credentialVersion {
		return nil, errors.New("invalid credential ciphertext")
	}
	nonce := ciphertext[1 : 1+c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, ciphertext[1+c.aead.NonceSize():], []byte("necly-inventory-credentials-v1"))
	if err != nil {
		return nil, errors.New("credential authentication failed")
	}
	return plain, nil
}
