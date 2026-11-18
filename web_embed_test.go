package main

import (
	"io/fs"
	"strings"
	"testing"
)

// Prüft die echte Einbettung (keine Attrappe): die Oberfläche muss unter index.html lesbar sein
func TestOberflaecheIstEingebettet(t *testing.T) {
	daten, err := fs.ReadFile(uiDateien, "index.html")
	if err != nil {
		t.Fatalf("index.html nicht lesbar: %v", err)
	}
	if !strings.Contains(string(daten), "Daten-Werkzeug") {
		t.Fatal("eingebettete index.html hat nicht den erwarteten Inhalt")
	}
}
