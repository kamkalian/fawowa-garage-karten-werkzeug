package main

// Höhenmodell aus SRTM-Kacheln (*.hgt.gz, 3601 × 3601 Werte je Grad-Kachel, big-endian). Bilineare Abfrage in Metern.
// Entspricht tools/routing/hoehen.js.

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

const hgtN = 3601
const hgtLeer = -32768

type hoehen struct {
	verzeichnis string
	kacheln     map[string][]int16 // nil: keine Datei dafür
}

func neueHoehen(verzeichnis string) *hoehen {
	return &hoehen{verzeichnis: verzeichnis, kacheln: map[string][]int16{}}
}

func (h *hoehen) kachel(lat, lon int) ([]int16, error) {
	schluessel := fmt.Sprintf("%d,%d", lat, lon)
	if k, ok := h.kacheln[schluessel]; ok {
		return k, nil
	}
	buch := "N"
	if lat < 0 {
		buch = "S"
	}
	ost := "E"
	if lon < 0 {
		ost = "W"
	}
	name := fmt.Sprintf("%s%02d%s%03d", buch, absInt(lat), ost, absInt(lon))
	datei := filepath.Join(h.verzeichnis, name+".hgt.gz")
	var daten []int16
	if _, err := os.Stat(datei); err == nil {
		roh, err := ladeHgt(datei)
		if err != nil {
			return nil, err
		}
		if len(roh) != hgtN*hgtN*2 {
			return nil, fmt.Errorf("%s: unerwartete Größe %d", name, len(roh))
		}
		daten = make([]int16, hgtN*hgtN)
		for i := range daten {
			daten[i] = int16(uint16(roh[2*i])<<8 | uint16(roh[2*i+1]))
		}
	}
	h.kacheln[schluessel] = daten
	return daten, nil
}

func ladeHgt(datei string) ([]byte, error) {
	f, err := os.Open(datei)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// hoehe liefert die Höhe in Metern an einer Stelle (Grad) oder NaN, wenn es dort keine Daten gibt
func (h *hoehen) hoehe(lat, lon float64) float64 {
	kl := int(math.Floor(lat))
	ko := int(math.Floor(lon))
	k, err := h.kachel(kl, ko)
	if err != nil || k == nil {
		return math.NaN()
	}
	zeile := (float64(kl) + 1 - lat) * (hgtN - 1)
	spalte := (lon - float64(ko)) * (hgtN - 1)
	z0 := minInt(hgtN-2, int(math.Floor(zeile)))
	s0 := minInt(hgtN-2, int(math.Floor(spalte)))
	fz := zeile - float64(z0)
	fs := spalte - float64(s0)
	a := float64(k[z0*hgtN+s0])
	b := float64(k[z0*hgtN+s0+1])
	c := float64(k[(z0+1)*hgtN+s0])
	d := float64(k[(z0+1)*hgtN+s0+1])
	aL := k[z0*hgtN+s0] == hgtLeer
	bL := k[z0*hgtN+s0+1] == hgtLeer
	cL := k[(z0+1)*hgtN+s0] == hgtLeer
	dL := k[(z0+1)*hgtN+s0+1] == hgtLeer
	if aL || bL || cL || dL {
		// Lücke im Datensatz: Mittel der gültigen Nachbarwerte
		summe := 0.0
		n := 0
		for _, p := range []struct {
			wert float64
			leer bool
		}{{a, aL}, {b, bL}, {c, cL}, {d, dL}} {
			if !p.leer {
				summe += p.wert
				n++
			}
		}
		if n == 0 {
			return math.NaN()
		}
		return summe / float64(n)
	}
	return (a*(1-fs)+b*fs)*(1-fz) + (c*(1-fs)+d*fs)*fz
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
