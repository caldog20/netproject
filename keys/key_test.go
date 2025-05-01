package keys

import (
	"testing"
)

func TestPublicKeyEncodeDecodeString(t *testing.T) {
	k := NewPrivateKey()
	pub := k.PublicKey()

	pubString := pub.EncodeToString()

	newPub := PublicKey{}
	if err := newPub.DecodeFromString(pubString); err != nil {
		t.Fatal(err)
	}

	if pub != newPub {
		t.Fatalf("got %x expected %x", pub.k, newPub.k)
	}
}

func TestPublicKeyFromRawBytes(t *testing.T) {
	k := NewPrivateKey()
	pub := k.PublicKey()

	bytes := pub.Raw()

	newPub := NewPublicKeyFromRawBytes(bytes)
	if pub != newPub {
		t.Fatalf("got %x expected %x", pub.k, newPub.k)
	}
}
