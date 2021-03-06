package crypto

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"testing"
)

// CONTRIBUTE: Additional tests could be used, such as testing that decryption
// fails if the wrong iv's are used, and overall trying to probe the library
// for something that doesn't work quite right.

// TestEncryption makes sure that things can be encrypted and decrypted.
func TestEncryption(t *testing.T) {
	// Get a key for encryption.
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt the zero plaintext.
	plaintext := make([]byte, 128)
	_, err = rand.Read(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, iv, padding, err := EncryptBytes(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Get the decrypted plaintext.
	decryptedPlaintext, err := DecryptBytes(key, ciphertext, iv, padding)
	if err != nil {
		t.Fatal(err)
	}

	// Compare the original to the decrypted.
	if bytes.Compare(plaintext, decryptedPlaintext) != 0 {
		t.Fatal("Encrypted and decrypted zero plaintext do not match")
	}

	// Try to decrypt using a different key
	key2, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	badtext, err := DecryptBytes(key2, ciphertext, iv, padding)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(plaintext, badtext) == 0 {
		t.Fatal("When using the wrong key, plaintext was still decrypted!")
	}
}

// TestPadding encrypts and decrypts a byte slice that invokes every possible
// padding length.
func TestPadding(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	// Encrypt and decrypt for all of the potential padded values and see that
	// padding is handled correctly.
	for i := 256; i < 256+BlockSize; i++ {
		key, err := GenerateEncryptionKey()
		if err != nil {
			t.Fatal(err)
		}
		plaintext := make([]byte, i)
		rand.Read(plaintext)
		ciphertext, iv, padding, err := EncryptBytes(key, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		decryptedPlaintext, err := DecryptBytes(key, ciphertext, iv, padding)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Compare(plaintext, decryptedPlaintext) != 0 {
			t.Fatal("Encrypted and decrypted zero plaintext do not match for i = ", i)
		}
	}
}

// TestEntropy encrypts and then decrypts a zero plaintext, checking that the
// ciphertext is high entropy. This is simply to check for obvious mistakes and
// not to guarantee security of the ciphertext.
func TestEntropy(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	// Encrypt a larger zero plaintext and make sure that the outcome is high
	// entropy. We measure entropy by seeing how much gzip can compress the
	// ciphertext. 10 * 1000 bytes was chosen because gzip overhead will exceed
	// compression rate for smaller files, even low entropy files.
	cipherSize := 10 * 1000
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	plaintext := make([]byte, cipherSize)
	ciphertext, _, _, err := EncryptBytes(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Gzip the ciphertext
	var b bytes.Buffer
	zip := gzip.NewWriter(&b)
	zip.Write(ciphertext)
	zip.Close()
	if b.Len() < cipherSize {
		t.Error("supposedly high entropy ciphertext has been compressed!")
	}
}
