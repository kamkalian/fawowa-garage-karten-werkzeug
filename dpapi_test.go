//go:build windows

package main

import "testing"

func TestDPAPIRundreise(t *testing.T) {
	geheim, err := Verschluesseln("Passwort-Test-123")
	if err != nil {
		t.Fatal(err)
	}
	if geheim == "Passwort-Test-123" || contains(geheim, "Passwort-Test") {
		t.Fatal("Klartext nach dem Verschlüsseln sichtbar")
	}
	klar, err := Entschluesseln(geheim)
	if err != nil {
		t.Fatal(err)
	}
	if klar != "Passwort-Test-123" {
		t.Fatalf("falsch entschlüsselt: %q", klar)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
