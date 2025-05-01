package keys

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"

	"golang.org/x/crypto/curve25519"
)

const (
	PublicKeyLen = 32
)

type NoCompare [0]func()

type PrivateKey struct {
	_ NoCompare
	k [32]byte
}

func NewPrivateKey() PrivateKey {
	k := [32]byte{}
	if _, err := io.ReadFull(rand.Reader, k[:]); err != nil {
		panic("error generating random bytes for private key: " + err.Error())
	}

	// clamp
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return PrivateKey{k: k}
}

func (k PrivateKey) IsZero() bool {
	return k.Compare(PrivateKey{})
}

func (k PrivateKey) Compare(other PrivateKey) bool {
	return subtle.ConstantTimeCompare(k.k[:], other.k[:]) == 1
}

func (k PrivateKey) MarshalText() ([]byte, error) {
	b := make([]byte, base64.StdEncoding.EncodedLen(len(k.k)))
	base64.StdEncoding.Encode(b, k.k[:])
	return b, nil
}

func (k *PrivateKey) UnmarshalText(text []byte) error {
	_, err := base64.StdEncoding.Decode(k.k[:], text)
	return err
}

func (k PrivateKey) PublicKey() PublicKey {
	pub := PublicKey{}
	curve25519.ScalarBaseMult(&pub.k, &k.k)
	return pub
}

type PublicKey struct {
	k [32]byte
}

// Learned about mem.RO and friends from Tailscale.
// Might be able to use them here for cheap copies
func NewPublicKeyFromRawBytes(raw []byte) PublicKey {
	var key PublicKey
	copy(key.k[:], raw)
	return key
}

func (k PublicKey) MarshalText() ([]byte, error) {
	b := make([]byte, base64.StdEncoding.EncodedLen(len(k.k)))
	base64.StdEncoding.Encode(b, k.k[:])
	return b, nil
}

func (k *PublicKey) UnmarshalText(text []byte) error {
	_, err := base64.StdEncoding.Decode(k.k[:], text)
	if err != nil {
		return err
	}
	return nil
}

func (k *PublicKey) DecodeFromString(s string) error {
	if b, err := base64.StdEncoding.DecodeString(s); err != nil {
		return err
	} else {
		copy(k.k[:], b)
		return nil
	}
}

func (k PublicKey) EncodeToString() string {
	return base64.StdEncoding.EncodeToString(k.k[:])
}

func (k PublicKey) Raw() []byte {
	return bytes.Clone(k.k[:])
}

func (k PublicKey) IsZero() bool {
	return k == PublicKey{}
}
