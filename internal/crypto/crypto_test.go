package crypto

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	k, err := GenerateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	kb, err := k.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	plaintexts := []string{"hello", "", "line with spaces", "unicode: 世界 🎉", "long " + strings.Repeat("x", 10000)}
	for _, p := range plaintexts {
		enc, err := Encrypt(kb, []byte(p))
		if err != nil {
			t.Fatalf("encrypt %q: %v", p, err)
		}
		if strings.Contains(enc, "\n") {
			t.Errorf("encrypted form contains newline: %q", enc)
		}
		dec, err := Decrypt(kb, enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", p, err)
		}
		if string(dec) != p {
			t.Errorf("roundtrip mismatch: got %q, want %q", dec, p)
		}
	}
}

func TestDecryptWrongKey(t *testing.T) {
	k1, _ := GenerateKey("a")
	k2, _ := GenerateKey("b")
	b1, _ := k1.Bytes()
	b2, _ := k2.Bytes()

	enc, err := Encrypt(b1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(b2, enc); err == nil {
		t.Fatalf("decrypt with wrong key should have failed")
	}
}

func TestDecryptTampered(t *testing.T) {
	k, _ := GenerateKey("t")
	kb, _ := k.Bytes()
	enc, _ := Encrypt(kb, []byte("secret"))

	// Flip a char in the base64 body.
	if len(enc) < 10 {
		t.Fatal("encoded string unexpectedly short")
	}
	bad := enc[:5] + "A" + enc[6:]
	if bad == enc {
		bad = enc[:5] + "B" + enc[6:]
	}
	if _, err := Decrypt(kb, bad); err == nil {
		t.Fatalf("tampered ciphertext must fail auth")
	}
}

func TestNonceIsRandom(t *testing.T) {
	k, _ := GenerateKey("r")
	kb, _ := k.Bytes()
	a, _ := Encrypt(kb, []byte("same"))
	b, _ := Encrypt(kb, []byte("same"))
	if a == b {
		t.Fatalf("same plaintext produced identical ciphertext — nonce reuse")
	}
}

func TestEncryptingReaderAndDecryptCopy(t *testing.T) {
	k, _ := GenerateKey("stream")
	kb, _ := k.Bytes()

	in := "alpha\nbeta\ngamma\n"
	r := EncryptingReader(strings.NewReader(in), kb)

	// Read all encrypted output.
	var encrypted bytes.Buffer
	if _, err := encrypted.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(encrypted.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 encrypted lines, got %d: %v", len(lines), lines)
	}

	// Round-trip: feed the encrypted text back through DecryptCopy.
	var decrypted bytes.Buffer
	if err := DecryptCopy(&decrypted, &encrypted, kb); err != nil {
		t.Fatal(err)
	}
	if decrypted.String() != in {
		t.Fatalf("roundtrip: got %q, want %q", decrypted.String(), in)
	}
}
