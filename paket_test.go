package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// testDaten erzeugt eine deterministische Datei der Größe n
func testDaten(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31) ^ seed ^ byte(i>>8)
	}
	return b
}

func schreibe(t *testing.T, pfad string, daten []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pfad, daten, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Kleine Grenze für Tests: 1 MB Reserve, also bleiben 1,5 MB je ZIP
const testGrenze = 2_500_000

func paketFuerTest(t *testing.T) (quelle, ziel string, dateien []string, manifest string) {
	t.Helper()
	quelle = t.TempDir()
	ziel = t.TempDir()
	schreibe(t, filepath.Join(quelle, "a.bin"), testDaten(3_200_000, 1))
	schreibe(t, filepath.Join(quelle, "b.bin"), testDaten(400_000, 2))
	schreibe(t, filepath.Join(quelle, "leer.bin"), []byte{})
	schreibe(t, filepath.Join(quelle, "karte", "c.txt"), testDaten(10_000, 3))
	dateien = []string{"a.bin", "b.bin", "leer.bin", "karte/c.txt"}
	manifest, err := Packen("netz", "2026-11-18T0815", quelle, ziel, dateien, testGrenze, map[string]string{"osm": "test"})
	if err != nil {
		t.Fatal(err)
	}
	return quelle, ziel, dateien, manifest
}

func TestPaketRundreise(t *testing.T) {
	quelle, ziel, dateien, manifest := paketFuerTest(t)
	zips, _ := filepath.Glob(filepath.Join(ziel, "*.zip"))
	if len(zips) < 3 {
		t.Fatalf("erwartet mindestens 3 ZIPs, es sind %d", len(zips))
	}
	for _, z := range zips {
		info, _ := os.Stat(z)
		if info.Size() > testGrenze {
			t.Errorf("%s ist %d Bytes, die Grenze ist %d", z, info.Size(), testGrenze)
		}
	}
	ausgepackt := t.TempDir()
	m, err := Auspacken(manifest, ziel, ausgepackt)
	if err != nil {
		t.Fatal(err)
	}
	if m.Art != "netz" || m.Version != "2026-11-18T0815" {
		t.Fatalf("Manifest falsch: %+v", m)
	}
	for _, d := range dateien {
		a, _ := os.ReadFile(filepath.Join(quelle, filepath.FromSlash(d)))
		b, err := os.ReadFile(filepath.Join(ausgepackt, filepath.FromSlash(d)))
		if err != nil {
			t.Fatalf("%s fehlt nach dem Auspacken: %v", d, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s ist nach dem Auspacken nicht identisch", d)
		}
	}
}

func TestPaketEinStueckGenauSoGross(t *testing.T) {
	quelle := t.TempDir()
	ziel := t.TempDir()
	// Genau so groß wie der nutzbare Platz einer ZIP
	schreibe(t, filepath.Join(quelle, "genau.bin"), testDaten(testGrenze-paketReserve, 4))
	manifest, err := Packen("karte", "v1", quelle, ziel, []string{"genau.bin"}, testGrenze, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Auspacken(manifest, ziel, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestPaketLeereDatei(t *testing.T) {
	quelle := t.TempDir()
	ziel := t.TempDir()
	schreibe(t, filepath.Join(quelle, "leer.bin"), []byte{})
	manifest, err := Packen("karte", "v1", quelle, ziel, []string{"leer.bin"}, testGrenze, nil)
	if err != nil {
		t.Fatal(err)
	}
	aus := t.TempDir()
	if _, err := Auspacken(manifest, ziel, aus); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(aus, "leer.bin"))
	if err != nil || info.Size() != 0 {
		t.Fatalf("leere Datei fehlt oder ist nicht leer: %v", err)
	}
}

func TestPaketFehlendeZipWirdErkannt(t *testing.T) {
	_, ziel, _, manifest := paketFuerTest(t)
	zips, _ := filepath.Glob(filepath.Join(ziel, "*.zip"))
	os.Remove(zips[0])
	if _, err := Auspacken(manifest, ziel, t.TempDir()); err == nil {
		t.Fatal("fehlende ZIP wurde nicht erkannt")
	}
}

func TestPaketVeraenderteZipWirdErkannt(t *testing.T) {
	_, ziel, _, manifest := paketFuerTest(t)
	zips, _ := filepath.Glob(filepath.Join(ziel, "*.zip"))
	daten, _ := os.ReadFile(zips[1])
	daten[len(daten)/2] ^= 0xFF
	os.WriteFile(zips[1], daten, 0o644)
	if _, err := Auspacken(manifest, ziel, t.TempDir()); err == nil {
		t.Fatal("veränderte ZIP wurde nicht erkannt")
	}
}

func TestPfadWirdGeprueft(t *testing.T) {
	for _, p := range []string{"../etc/passwd", "/abs/pfad", "a/../b", "Gross.bin", "a b.bin", ""} {
		if err := PfadErlaubt(p); err == nil {
			t.Errorf("Pfad %q wurde nicht abgelehnt", p)
		}
	}
	for _, p := range []string{"germany.pmtiles", "karte/pts.i32", "knoten_lat.i32"} {
		if err := PfadErlaubt(p); err != nil {
			t.Errorf("Pfad %q wurde abgelehnt: %v", p, err)
		}
	}
	quelle := t.TempDir()
	schreibe(t, filepath.Join(quelle, "x.bin"), []byte("x"))
	if _, err := Packen("netz", "v1", quelle, t.TempDir(), []string{"../x.bin"}, testGrenze, nil); err == nil {
		t.Fatal("Packen hat einen Pfad mit .. angenommen")
	}
}

// Erzeugt das gemeinsame Beispielpaket für die Node-Tests (nur mit der Umgebungsvariable PAKET_BEISPIEL)
func TestPaketBeispielErzeugen(t *testing.T) {
	ziel := os.Getenv("PAKET_BEISPIEL")
	if ziel == "" {
		t.Skip("PAKET_BEISPIEL nicht gesetzt")
	}
	quelle := t.TempDir()
	schreibe(t, filepath.Join(quelle, "info.json"), []byte(`{"bbox":{"minLat":47,"maxLat":55,"minLon":5,"maxLon":15}}`))
	schreibe(t, filepath.Join(quelle, "knoten_lat.i32"), testDaten(40_000, 5))
	schreibe(t, filepath.Join(quelle, "karte", "pts.i32"), testDaten(60_000, 6))
	_, err := Packen("netz", "2026-11-18T0815", quelle, ziel, []string{"info.json", "knoten_lat.i32", "karte/pts.i32"}, testGrenze, map[string]string{"osm": "beispiel"})
	if err != nil {
		t.Fatal(err)
	}
}
