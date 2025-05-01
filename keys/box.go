package keys

import (
	crand "crypto/rand"
	"io"

	"golang.org/x/crypto/nacl/box"
)

const (
	NonceLen    int = 24
	BoxOverhead int = 16
)

type nonce = [NonceLen]byte

func (k PrivateKey) EncryptBox(data []byte, receiverKey PublicKey) []byte {
	var n nonce
	if _, err := io.ReadFull(crand.Reader, n[:]); err != nil {
		panic("cannot generate random bytes from box nonce")
	}
	return box.Seal(n[:], data, &n, &receiverKey.k, &k.k)
}

func (k PrivateKey) DecryptBox(nonceAndData []byte, senderKey PublicKey) ([]byte, bool) {
	// If the slice isn't atleast 24 bytes then we can't decrypt even an empty message
	// since the nonce is 24 bytes
	if len(nonceAndData) < NonceLen {
		return nil, false
	}
	var n nonce
	copy(n[:], nonceAndData[:24])
	return box.Open(nil, nonceAndData[24:], &n, &senderKey.k, &k.k)
}

func DecryptBox(nonceAndData []byte, senderKey PublicKey, receiverKey PrivateKey) ([]byte, bool) {
	var n nonce
	copy(n[:], nonceAndData[:24])
	return box.Open(nil, nonceAndData[24:], &n, &senderKey.k, &receiverKey.k)
}

func EncryptBox(data []byte, senderKey PrivateKey, receiverKey PublicKey) []byte {
	var n nonce
	if _, err := io.ReadFull(crand.Reader, n[:]); err != nil {
		panic("cannot generate random bytes from box nonce")
	}
	return box.Seal(n[:], data, &n, &receiverKey.k, &senderKey.k)
}
