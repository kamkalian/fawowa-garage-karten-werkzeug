package main

// Prüfung gegen die Node-Version: die Tests laufen nur mit Umgebungsvariablen, weil sie die echten Kartendaten brauchen.
//   DATEN_PBF=...        Pfad zur PBF-Datei (z. B. nordrhein-westfalen)
//   DATEN_HOEHEN=...     Ordner mit den .hgt.gz-Dateien
//   KARTE_REF=...        Ordner mit der Karte der Node-Version (Dateien pts.i32, off.u32, meta.u8, bbox.f32)
//   NETZ_REF=...         Ordner mit dem Wegenetz der Node-Version
// Aufruf:  go test -run Vergleich -v

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func vergleicheDateien(t *testing.T, ausgabe, referenz string, namen []string) {
	t.Helper()
	for _, n := range namen {
		a, err := os.ReadFile(filepath.Join(ausgabe, n))
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		b, err := os.ReadFile(filepath.Join(referenz, n))
		if err != nil {
			t.Errorf("Referenz %s: %v", n, err)
			continue
		}
		if bytes.Equal(a, b) {
			t.Logf("%s: identisch (%d Bytes)", n, len(a))
			continue
		}
		unterschiede := 0
		erstes := -1
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				unterschiede++
				if erstes < 0 {
					erstes = i
				}
			}
		}
		t.Errorf("%s: unterschiedlich (Länge %d gegen %d, %d abweichende Bytes, erstes bei %d)", n, len(a), len(b), unterschiede, erstes)
	}
}

func ausgabeOrdner(t *testing.T) string {
	if d := os.Getenv("AUSGABE"); d != "" {
		os.MkdirAll(d, 0o755)
		return d
	}
	return t.TempDir()
}

func TestVergleichKarte(t *testing.T) {
	pbf, ref := os.Getenv("DATEN_PBF"), os.Getenv("KARTE_REF")
	if pbf == "" || ref == "" {
		t.Skip("DATEN_PBF und KARTE_REF nicht gesetzt")
	}
	ausgabe := ausgabeOrdner(t)
	if err := karteBauen(pbf, ausgabe, func(f string, a ...any) { t.Logf(f, a...) }); err != nil {
		t.Fatal(err)
	}
	vergleicheDateien(t, filepath.Join(ausgabe, "karte"), ref, []string{"pts.i32", "off.u32", "meta.u8", "bbox.f32"})
}

func TestVergleichNetz(t *testing.T) {
	pbf, hoehen, ref := os.Getenv("DATEN_PBF"), os.Getenv("DATEN_HOEHEN"), os.Getenv("NETZ_REF")
	if pbf == "" || hoehen == "" || ref == "" {
		t.Skip("DATEN_PBF, DATEN_HOEHEN und NETZ_REF nicht gesetzt")
	}
	ausgabe := ausgabeOrdner(t)
	if err := netzBauen(pbf, hoehen, ausgabe, func(f string, a ...any) { t.Logf(f, a...) }); err != nil {
		t.Fatal(err)
	}
	namen := []string{
		"knoten_lat.i32", "knoten_lon.i32", "adj_start.u32", "adj.u32", "kante_a.u32", "kante_b.u32",
		"kante_flags.u8", "kante_belag.u8", "kante_leg_start.u32", "kante_leg_n.u8",
		"leg_len.u16", "leg_grad.i8", "leg_kurs.u8", "knoten_typ.u8", "orte.json",
	}
	vergleicheDateien(t, ausgabe, ref, namen)
}
