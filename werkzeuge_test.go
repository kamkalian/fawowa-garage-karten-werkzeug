package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Das ZIP-Paket muss sich mit den Standard-Werkzeugen wieder öffnen lassen (der Server packt es aus)
func TestZipRundreise(t *testing.T) {
	quelle := t.TempDir()
	os.MkdirAll(filepath.Join(quelle, "karte"), 0o755)
	os.WriteFile(filepath.Join(quelle, "info.json"), []byte(`{"bbox":{}}`), 0o644)
	os.WriteFile(filepath.Join(quelle, "karte", "pts.i32"), []byte{1, 2, 3, 250}, 0o644)
	ziel := filepath.Join(t.TempDir(), "netz.zip")
	if err := zippen(quelle, ziel); err != nil {
		t.Fatal(err)
	}
	leser, err := zip.OpenReader(ziel)
	if err != nil {
		t.Fatal(err)
	}
	defer leser.Close()
	gefunden := map[string][]byte{}
	for _, f := range leser.File {
		r, _ := f.Open()
		var b bytes.Buffer
		io.Copy(&b, r)
		r.Close()
		gefunden[f.Name] = b.Bytes()
	}
	if !bytes.Equal(gefunden["karte/pts.i32"], []byte{1, 2, 3, 250}) {
		t.Fatalf("Inhalt stimmt nicht: %v", gefunden)
	}
	if string(gefunden["info.json"]) != `{"bbox":{}}` {
		t.Fatalf("info.json stimmt nicht")
	}
}

// Ein Download einer Höhenkachel (kleine Datei, ca. 10 MB) über das Netz
func TestDownloadHoehe(t *testing.T) {
	if os.Getenv("NETZTEST") == "" {
		t.Skip("NETZTEST nicht gesetzt")
	}
	ziel := filepath.Join(t.TempDir(), "N47E006.hgt.gz")
	url := hoehenUrl + "/N47/N47E006.hgt.gz"
	geladen, fehlt, err := ladeDatei(url, ziel, true, false, "Test", func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if !geladen || fehlt {
		t.Fatalf("geladen %v, fehlt %v", geladen, fehlt)
	}
	info, _ := os.Stat(ziel)
	if info.Size() < 1000000 {
		t.Fatalf("Datei zu klein: %d", info.Size())
	}
	if _, err := os.Stat(ziel + ".stand.json"); err != nil {
		t.Fatalf("Stand-Datei fehlt")
	}
	// zweiter Lauf: aktuell, nichts wird geladen
	geladen2, _, err := ladeDatei(url, ziel, true, false, "Test", func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if geladen2 {
		t.Fatalf("zweiter Lauf hat unnötig neu geladen")
	}
}
