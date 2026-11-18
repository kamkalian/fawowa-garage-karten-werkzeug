package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanetilerBefehl(t *testing.T) {
	args := planetilerArgumente("p.jar", "osm.pbf", "out/germany.pmtiles", "q", 8, 14, true)
	s := strings.Join(args, " ")
	for _, teil := range []string{"-Xmx8g", "-jar p.jar", "--osm-path=osm.pbf", "--output=out/germany.pmtiles", "--download", "--maxzoom=14", "--force", "--storage=mmap"} {
		if !strings.Contains(s, teil) {
			t.Errorf("%q fehlt in %q", teil, s)
		}
	}
	ohne := strings.Join(planetilerArgumente("p.jar", "a", "b", "q", 4, 12, false), " ")
	if strings.Contains(ohne, "--storage") {
		t.Error("--storage darf ohne mmap nicht gesetzt sein")
	}
}

func TestEntpackeZipWehrtPfadAus(t *testing.T) {
	dir := t.TempDir()
	zipDatei := filepath.Join(dir, "boes.zip")
	f, _ := os.Create(zipDatei)
	w := zip.NewWriter(f)
	ziel, _ := w.Create("../ausserhalb.txt")
	ziel.Write([]byte("x"))
	w.Close()
	f.Close()
	if err := entpackeZip(zipDatei, filepath.Join(dir, "ziel")); err == nil {
		t.Fatal("Pfad aus dem Zielordner wurde nicht abgelehnt")
	}
	if _, err := os.Stat(filepath.Join(dir, "ausserhalb.txt")); err == nil {
		t.Fatal("Datei außerhalb des Zielordners wurde geschrieben")
	}
}

func TestEntpackeZipNormal(t *testing.T) {
	dir := t.TempDir()
	zipDatei := filepath.Join(dir, "ok.zip")
	f, _ := os.Create(zipDatei)
	w := zip.NewWriter(f)
	e, _ := w.Create("bin/java.exe")
	e.Write([]byte("java"))
	w.Close()
	f.Close()
	ziel := filepath.Join(dir, "ziel")
	if err := entpackeZip(zipDatei, ziel); err != nil {
		t.Fatal(err)
	}
	if j := findeJava(ziel); j == "" || !strings.HasSuffix(j, "java.exe") {
		t.Fatalf("java.exe nicht gefunden: %q", j)
	}
}

func TestNeuestesPaket(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"netz-2026-11-01T0800", "netz-2026-11-18T0815", "karte-2026-12-01T0900"} {
		os.MkdirAll(filepath.Join(dir, n), 0o755)
	}
	if got := neuestesPaket(dir, "netz-"); got != "netz-2026-11-18T0815" {
		t.Errorf("neuestes netz: %q", got)
	}
	if got := neuestesPaket(dir, "karte-"); got != "karte-2026-12-01T0900" {
		t.Errorf("neuestes karte: %q", got)
	}
	if got := neuestesPaket(dir, "fehlt-"); got != "" {
		t.Errorf("fehlendes Präfix: %q", got)
	}
}
