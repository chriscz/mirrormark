package chrome

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/chriscz/mirrormark/internal/db"
)

// LoadKey reads the install's private key.
func LoadKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an RSA key", path)
	}
	return rk, nil
}

// LoadOrCreateKey returns the install's key, creating it on first use.
// Keeping it means an upgrade keeps the extension id; not shipping one
// means an extension built elsewhere cannot take this install's id. A key
// file that exists but cannot be read is an error, never replaced: a new
// key would be a new extension.
func LoadOrCreateKey(path string) (*rsa.PrivateKey, error) {
	k, err := LoadKey(path)
	if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	k, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return k, db.WriteAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

// PublicKeyDER is k's public key as DER SubjectPublicKeyInfo, the form
// Chrome takes in a manifest's "key" and a CRX header.
func PublicKeyDER(k *rsa.PrivateKey) []byte {
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey) // cannot fail for an RSA key
	return der
}

// ExtensionID is Chrome's id for a public key: the first 16 bytes of its
// SHA-256, each nibble written as a letter from a to p.
func ExtensionID(publicKeyDER []byte) string {
	sum := sha256.Sum256(publicKeyDER)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i], id[2*i+1] = 'a'+b>>4, 'a'+b&0xf
	}
	return string(id)
}
