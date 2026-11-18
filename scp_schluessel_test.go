package main

import (
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testSchluesselDatei schreibt den privaten Schlüssel als OpenSSH-Datei für die Tests
func testSchluesselDatei(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(t.TempDir(), "schluessel")
	if err := os.WriteFile(pfad, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return pfad
}
