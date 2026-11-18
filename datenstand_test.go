package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGroesseText(t *testing.T) {
	if g := groesseText(4_900_000_000); !strings.HasSuffix(g, "GB") || !strings.Contains(g, ",") {
		t.Fatalf("GB-Anzeige falsch: %q", g)
	}
	if g := groesseText(5 << 20); g != "5 MB" {
		t.Fatalf("MB-Anzeige falsch: %q", g)
	}
}

func TestDatenStandOsmUndHoehen(t *testing.T) {
	arbeit := t.TempDir()
	if s := datenStand("osm", arbeit); s.Vorhanden {
		t.Fatal("ohne Datei darf nichts vorhanden sein")
	}
	os.MkdirAll(filepath.Join(arbeit, "roh"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"), make([]byte, 1000), 0o644)
	if s := datenStand("osm", arbeit); !s.Vorhanden || s.Seit == "" {
		t.Fatalf("OSM-Stand fehlt: %+v", s)
	}
	os.MkdirAll(filepath.Join(arbeit, "hoehen"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "hoehen", "N50E006.hgt.gz"), make([]byte, 10), 0o644)
	if s := datenStand("hoehen", arbeit); !s.Vorhanden || s.Detail != "1 Kacheln" {
		t.Fatalf("Höhen-Stand falsch: %+v", s)
	}
}

func TestHochladenZeigtFehlendeUndHochgeladenePakete(t *testing.T) {
	arbeit := t.TempDir()
	os.MkdirAll(filepath.Join(arbeit, "pakete", "netz-2026-11-18T0815"), 0o755)
	os.MkdirAll(filepath.Join(arbeit, "pakete", "karte-2026-11-18T0900"), 0o755)
	s := datenStand("hochladen", arbeit)
	if !s.Vorhanden || !strings.Contains(s.Detail, "noch nicht hochgeladen") {
		t.Fatalf("vor dem Upload sollte es fehlen: %+v", s)
	}
	paketHochgeladenNotieren(arbeit, "netz-2026-11-18T0815")
	s = datenStand("hochladen", arbeit)
	if !strings.Contains(s.Detail, "auf dem Server: netz-2026-11-18T0815") || !strings.Contains(s.Detail, "noch nicht hochgeladen: karte-2026-11-18T0900") {
		t.Fatalf("nach einem Upload falsch: %+v", s)
	}
}

func TestLetzterLaufWirdGespeichert(t *testing.T) {
	arbeit := t.TempDir()
	letzterLaufNotieren(arbeit, "osm", time.Now().Add(-90*time.Second), "fertig", "")
	s := schrittStandLesen(arbeit, "osm")
	if s.Letzter == nil || s.Letzter.Ergebnis != "fertig" || s.Letzter.DauerSek < 89 {
		t.Fatalf("letzter Lauf falsch: %+v", s.Letzter)
	}
}

func TestStatusListeMeldetDatenUndLetztenLauf(t *testing.T) {
	arbeit := t.TempDir()
	os.MkdirAll(filepath.Join(arbeit, "roh"), 0o755)
	os.WriteFile(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"), make([]byte, 10), 0o644)
	letzterLaufNotieren(arbeit, "osm", time.Now(), "fertig", "")
	e := neueEngine(neuerHub(), func() Einstellungen { return Einstellungen{Arbeitsordner: arbeit} })
	for _, s := range e.StatusListe() {
		if s["id"] == "osm" {
			d := s["daten"].(DatenStand)
			if !d.Vorhanden || s["letzter"] == nil {
				t.Fatalf("OSM ohne Daten oder letzten Lauf: %+v", s)
			}
		}
		if s["id"] == "netz" && s["daten"].(DatenStand).Vorhanden {
			t.Fatal("Netz ist nicht gebaut und darf nicht als vorhanden gelten")
		}
	}
}
