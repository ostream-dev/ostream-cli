// Package crypto handles per-line ChaCha20-Poly1305 encryption for the CLI.
//
// Keys are stored locally as JSON under $HOME/.ostream/keys/<id>.json.
// The relay never sees plaintext or keys — encryption is entirely client-
// side, symmetric, and by convention shared out-of-band.
//
// Wire format (one encrypted line):
//
//	base64url(nonce || ciphertext)
//
// where nonce is 12 random bytes and ciphertext includes the 16-byte
// Poly1305 auth tag. A 100-byte plaintext therefore becomes ~170 chars
// of base64url — no newlines, safe as an ostream line.
package crypto

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/ostream-dev/ostream-cli/internal/config"
)

const (
	// Algo is the identifier stored in key files so old keys stay readable
	// if we ever add another algorithm.
	Algo = "chacha20-poly1305"
)

// Key is the persisted form of an encryption key. The raw bytes are base64url-
// encoded in the JSON so the file remains text.
type Key struct {
	ID   string `json:"id"`
	Algo string `json:"algo"`
	Key  string `json:"key"` // base64url of 32 raw bytes
}

// Bytes returns the raw key material.
func (k *Key) Bytes() ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(k.Key)
	if err != nil {
		return nil, fmt.Errorf("decode key %q: %w", k.ID, err)
	}
	if len(b) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("key %q is %d bytes, want %d", k.ID, len(b), chacha20poly1305.KeySize)
	}
	return b, nil
}

// GenerateKey returns a fresh random key tagged with the given ID.
func GenerateKey(id string) (*Key, error) {
	raw := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return &Key{
		ID:   id,
		Algo: Algo,
		Key:  base64.RawURLEncoding.EncodeToString(raw),
	}, nil
}

// KeyDir returns the directory where key files are stored.
func KeyDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keys"), nil
}

// KeyPath returns the full file path for a given key ID.
func KeyPath(id string) (string, error) {
	dir, err := KeyDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// LoadKey reads the key file for the given ID.
func LoadKey(id string) (*Key, error) {
	p, err := KeyPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var k Key
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if k.Algo != Algo {
		return nil, fmt.Errorf("key %q uses algo %q, only %q is supported", k.ID, k.Algo, Algo)
	}
	return &k, nil
}

// SaveKey writes a key file with mode 0600. Refuses to overwrite an
// existing file so a misspelled --id doesn't clobber a previous key.
func SaveKey(k *Key) (string, error) {
	p, err := KeyPath(k.ID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("mkdir key dir: %w", err)
	}
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("key %q already exists at %s", k.ID, p)
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}

// ListKeys returns the IDs of all keys in the key directory, sorted.
// Missing directory is treated as empty (no keys yet).
func ListKeys() ([]string, error) {
	dir, err := KeyDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if filepath.Ext(n) == ".json" {
			ids = append(ids, n[:len(n)-len(".json")])
		}
	}
	return ids, nil
}

// Encrypt returns base64url(nonce || ciphertext) of plaintext.
func Encrypt(keyBytes, plaintext []byte) (string, error) {
	aead, err := chacha20poly1305.New(keyBytes)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// Seal prepends the nonce (first arg) into the destination slice.
	combined := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(combined), nil
}

// Decrypt takes base64url(nonce || ciphertext) and returns the plaintext.
// Returns an error if authentication fails (wrong key or tampered data).
func Decrypt(keyBytes []byte, b64 string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	aead, err := chacha20poly1305.New(keyBytes)
	if err != nil {
		return nil, err
	}
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := raw[:aead.NonceSize()]
	ciphertext := raw[aead.NonceSize():]
	return aead.Open(nil, nonce, ciphertext, nil)
}

// EncryptingReader wraps src so that reads return newline-separated
// base64url-encoded ciphertext, one encrypted record per input line.
// Lines are processed in a goroutine through an io.Pipe; the reader
// returns io.EOF when src returns EOF.
func EncryptingReader(src io.Reader, keyBytes []byte) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		scanner := bufio.NewScanner(src)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			enc, err := Encrypt(keyBytes, scanner.Bytes())
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := fmt.Fprintln(pw, enc); err != nil {
				return // reader closed, stop writing
			}
		}
		if err := scanner.Err(); err != nil {
			pw.CloseWithError(err)
		}
	}()
	return pr
}

// DecryptCopy scans newline-separated base64url ciphertext from src,
// decrypts each line, and writes plaintext + "\n" to dst. Returns when
// src returns EOF. Any decrypt failure aborts immediately.
func DecryptCopy(dst io.Writer, src io.Reader, keyBytes []byte) error {
	scanner := bufio.NewScanner(src)
	// Allow for base64url overhead (~33% larger than plaintext).
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		pt, err := Decrypt(keyBytes, line)
		if err != nil {
			return fmt.Errorf("decrypt: %w", err)
		}
		if _, err := dst.Write(pt); err != nil {
			return err
		}
		if _, err := dst.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}
