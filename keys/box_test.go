package keys

import (
	"testing"
)

func TestBoxEncrypt(t *testing.T) {
	senderPriv := NewPrivateKey()
	senderPub := senderPriv.PublicKey()
	recvPriv := NewPrivateKey()
	recvPub := recvPriv.PublicKey()

	msg := "Hello"
	data := []byte(msg)
	expectedEncryptedLen := len(data) + NonceLen + BoxOverhead

	encrypted := senderPriv.EncryptBox(data, recvPub)
	if len(encrypted) != expectedEncryptedLen {
		t.Fatalf("got encrypted len %d, expected %d", len(encrypted), expectedEncryptedLen)
	}

	decrypted, ok := recvPriv.DecryptBox(encrypted, senderPub)
	if !ok {
		t.Fatal("decrypted of box failed")
	}

	if len(decrypted) != len(msg) {
		t.Fatalf("got uncrypted len %d, expected %d", len(decrypted), len(msg))
	}

	if string(decrypted) != "Hello" {
		t.Fatalf("got uncrypted %s, expected %s", string(decrypted), msg)
	}
}
