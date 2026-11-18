package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroesseText(t *testing.T) {
	if g := groesseText(4_900_000_000); !strings.HasSuffix(g, "GB") || !strings.Contains(g, ",") {
		t.Fatalf("GB-Anzeige falsch: %q", g)
	}
	if g := groesseText(5 << 20); g != "5 MB" {
		t.Fatalf("MB-Anzeige falsch: %q", g)
	}
}

func TestRohStandOsmUndHoehen(t *testing.T) {
	arbeit := t.TempDir()
	if rohStand("osm", arbeit) != nil {
		t.Fatal("ohne Datei darf es keinen Stand geben")
	}
	os.MkdirAll(filepath.Join(arbeit, "roh"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"), make([]byte, 1000), 0o644)
	if s := rohStand("osm", arbeit); s == nil || s.Seit == "" {
		t.Fatalf("OSM-Stand fehlt: %+v", s)
	}
	os.MkdirAll(filepath.Join(arbeit, "hoehen"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "hoehen", "N50E006.hgt.gz"), make([]byte, 10), 0o644)
	if s := rohStand("hoehen", arbeit); s == nil || s.Detail != "1 Kacheln" {
		t.Fatalf("Höhen-Stand falsch: %+v", s)
	}
}

func TestStatusListeMeldetVorhandeneRohdaten(t *testing.T) {
	arbeit := t.TempDir()
	os.MkdirAll(filepath.Join(arbeit, "roh"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"), make([]byte, 10), 0o644)
	e := neueEngine(neuerHub(), func() Einstellungen { return Einstellungen{Arbeitsordner: arbeit} })
	for _, s := range e.StatusListe() {
		if s["id"] == "osm" && s["roh"] == nil {
			t.Fatal("vorhandene OSM-Datei wird nicht gemeldet")
		}
		if s["id"] == "netz" && s["roh"] != nil {
			t.Fatal("Netz hat keine Rohdaten und darf keinen Stand melden")
		}
	}
}
