package main

// Paketformat „fawowa-paket-1“: Dateien (Wegenetz, Karte) werden in Stücke zerlegt und auf ZIP-Dateien verteilt, die höchstens
// PaketGrenze Bytes groß sind. Einträge sind unkomprimiert (Store). Beschreibung in docs/PAKETFORMAT.md.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	PaketFormat  = "fawowa-paket-1"
	PaketGrenze  = 200_000_000 // höchstens 200 MB je ZIP
	paketReserve = 1_000_000   // Platz für ZIP-Köpfe und das Manifest
	manifestName = "manifest.json"
)

// Manifest beschreibt ein Paket. Im ZIP steckt es ohne die Prüfsummen der ZIPs (die stehen erst fest, wenn die ZIP fertig ist).
type Manifest struct {
	Format  string            `json:"format"`
	Art     string            `json:"art"`
	Version string            `json:"version"`
	Erzeugt string            `json:"erzeugt"`
	Quelle  map[string]string `json:"quelle,omitempty"`
	Zips    []ZipEintrag      `json:"zips,omitempty"`
	Dateien []DateiEintrag    `json:"dateien"`
}

type ZipEintrag struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Sha256 string `json:"sha256,omitempty"`
}

type DateiEintrag struct {
	Pfad    string          `json:"pfad"`
	Bytes   int64           `json:"bytes"`
	Sha256  string          `json:"sha256"`
	Stuecke []StueckEintrag `json:"stuecke"`
}

type StueckEintrag struct {
	Zip     string `json:"zip"`
	Eintrag string `json:"eintrag"`
	Offset  int64  `json:"offset"`
	Bytes   int64  `json:"bytes"`
	Sha256  string `json:"sha256"`
}

var pfadMuster = regexp.MustCompile(`^[a-z0-9._-]+(/[a-z0-9._-]+)*$`)
var artMuster = regexp.MustCompile(`^(netz|karte)$`)
var versionMuster = regexp.MustCompile(`^[0-9A-Za-z._T-]+$`)

// PfadErlaubt prüft einen Pfad im Paket: nur a-z0-9._- und /, kein „..“, kein absoluter Pfad
func PfadErlaubt(p string) error {
	if !pfadMuster.MatchString(p) || strings.Contains(p, "..") || path.IsAbs(p) {
		return fmt.Errorf("ungültiger Pfad im Paket: %q", p)
	}
	return nil
}

func sha256Datei(pfad string) (string, int64, error) {
	f, err := os.Open(pfad)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// zipName: <art>-<version>.teil-NNN-von-MMM.zip
func zipName(art, version string, nr, anzahl int) string {
	return fmt.Sprintf("%s-%s.teil-%03d-von-%03d.zip", art, version, nr, anzahl)
}

// Packen zerlegt die Dateien (relative Pfade unter quelle) in ZIPs unter ziel. grenze: höchste Größe je ZIP.
// Ergebnis: Pfad des externen Manifests (<art>-<version>.manifest.json).
func Packen(art, version, quelle, ziel string, dateien []string, grenze int64, quellen map[string]string) (string, error) {
	if !artMuster.MatchString(art) {
		return "", fmt.Errorf("unbekannte Art: %q", art)
	}
	if !versionMuster.MatchString(version) {
		return "", fmt.Errorf("ungültige Version: %q", version)
	}
	if grenze <= 2*paketReserve {
		return "", fmt.Errorf("Grenze zu klein")
	}
	nutzbar := grenze - paketReserve
	if err := os.MkdirAll(ziel, 0o755); err != nil {
		return "", err
	}

	// 1. Prüfsummen und Stücke planen (ohne Daten zu lesen)
	m := Manifest{
		Format: PaketFormat, Art: art, Version: version,
		Erzeugt: time.Now().UTC().Format(time.RFC3339), Quelle: quellen,
	}
	var plan []geplant
	zipNr := 0
	belegt := int64(0)
	for _, d := range dateien {
		if err := PfadErlaubt(d); err != nil {
			return "", err
		}
		sum, groesse, err := sha256Datei(filepath.Join(quelle, filepath.FromSlash(d)))
		if err != nil {
			return "", err
		}
		de := DateiEintrag{Pfad: d, Bytes: groesse, Sha256: sum}
		// Leere Datei: ein Stück mit 0 Bytes, damit sie im Paket vorkommt
		rest := groesse
		off := int64(0)
		stueck := 0
		for {
			if belegt >= nutzbar {
				zipNr++
				belegt = 0
			}
			n := rest
			if n > nutzbar-belegt {
				n = nutzbar - belegt
			}
			plan = append(plan, geplant{zipNr: zipNr, eintrag: fmt.Sprintf("%s.%03d", d, stueck), offset: off, bytes: n, datei: d})
			de.Stuecke = append(de.Stuecke, StueckEintrag{Eintrag: fmt.Sprintf("%s.%03d", d, stueck), Offset: off, Bytes: n})
			belegt += n
			rest -= n
			off += n
			stueck++
			if rest == 0 {
				break
			}
		}
		m.Dateien = append(m.Dateien, de)
	}
	anzahl := zipNr + 1
	namen := make([]string, anzahl)
	for i := range namen {
		namen[i] = zipName(art, version, i+1, anzahl)
	}
	// Stücke den ZIPs zuordnen (Prüfsumme je Stück aus der Datei)
	stueckIdx := map[string]int{}
	for _, p := range plan {
		de := &m.Dateien[indexDatei(m.Dateien, p.datei)]
		idx := stueckIdx[p.datei]
		stueckIdx[p.datei]++
		de.Stuecke[idx].Zip = namen[p.zipNr]
		sum, err := sha256Stueck(filepath.Join(quelle, filepath.FromSlash(p.datei)), p.offset, p.bytes)
		if err != nil {
			return "", err
		}
		de.Stuecke[idx].Sha256 = sum
	}

	// 2. ZIPs schreiben
	inneres, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	var zipEintraege []ZipEintrag
	for i, name := range namen {
		pfad := filepath.Join(ziel, name)
		if err := schreibeZip(pfad, inneres, plan, i, quelle); err != nil {
			return "", err
		}
		sum, groesse, err := sha256Datei(pfad)
		if err != nil {
			return "", err
		}
		if groesse > grenze {
			return "", fmt.Errorf("%s ist %d Bytes groß, die Grenze ist %d", name, groesse, grenze)
		}
		zipEintraege = append(zipEintraege, ZipEintrag{Name: name, Bytes: groesse, Sha256: sum})
	}
	m.Zips = zipEintraege

	// 3. Externes Manifest mit den Prüfsummen der ZIPs
	ext, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	manifest := filepath.Join(ziel, fmt.Sprintf("%s-%s.manifest.json", art, version))
	if err := os.WriteFile(manifest, ext, 0o644); err != nil {
		return "", err
	}
	return manifest, nil
}

func indexDatei(dateien []DateiEintrag, pfad string) int {
	for i := range dateien {
		if dateien[i].Pfad == pfad {
			return i
		}
	}
	return -1
}

func sha256Stueck(pfad string, offset, bytes int64) (string, error) {
	f, err := os.Open(pfad)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, offset, bytes)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// geplant ist ein Stück, das in einer ZIP landet
type geplant struct {
	zipNr   int
	eintrag string
	offset  int64
	bytes   int64
	datei   string
}

// schreibeZip schreibt ZIP Nr. i: alle Stücke dieser ZIP (unkomprimiert) und das Manifest
func schreibeZip(pfad string, manifest []byte, plan []geplant, i int, quelle string) error {
	out, err := os.Create(pfad)
	if err != nil {
		return err
	}
	z := zip.NewWriter(out)
	for _, p := range plan {
		if p.zipNr != i {
			continue
		}
		f, err := os.Open(filepath.Join(quelle, filepath.FromSlash(p.datei)))
		if err != nil {
			out.Close()
			return err
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: p.eintrag, Method: zip.Store})
		if err != nil {
			f.Close()
			out.Close()
			return err
		}
		_, err = io.Copy(w, io.NewSectionReader(f, p.offset, p.bytes))
		f.Close()
		if err != nil {
			out.Close()
			return err
		}
	}
	w, err := z.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Store})
	if err != nil {
		out.Close()
		return err
	}
	if _, err := w.Write(manifest); err != nil {
		out.Close()
		return err
	}
	if err := z.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Auspacken prüft ein Paket (über das externe Manifest) und schreibt alle Dateien unter ziel. Vorhandene Dateien unter ziel
// werden überschrieben; der Aufrufer legt ziel als leeren Zwischenordner an.
func Auspacken(manifestPfad, zipOrdner, ziel string) (*Manifest, error) {
	var m Manifest
	daten, err := os.ReadFile(manifestPfad)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(daten, &m); err != nil {
		return nil, fmt.Errorf("Manifest ungültig: %w", err)
	}
	if m.Format != PaketFormat {
		return nil, fmt.Errorf("unbekanntes Format: %q", m.Format)
	}
	for _, z := range m.Zips {
		pfad := filepath.Join(zipOrdner, z.Name)
		sum, groesse, err := sha256Datei(pfad)
		if err != nil {
			return nil, fmt.Errorf("ZIP fehlt: %s", z.Name)
		}
		if groesse != z.Bytes || (z.Sha256 != "" && sum != z.Sha256) {
			return nil, fmt.Errorf("ZIP verändert oder unvollständig: %s", z.Name)
		}
	}
	offen := map[string]*zip.ReadCloser{}
	defer func() {
		for _, r := range offen {
			r.Close()
		}
	}()
	for _, d := range m.Dateien {
		if err := PfadErlaubt(d.Pfad); err != nil {
			return nil, err
		}
		ziel := filepath.Join(ziel, filepath.FromSlash(d.Pfad))
		if err := os.MkdirAll(filepath.Dir(ziel), 0o755); err != nil {
			return nil, err
		}
		out, err := os.Create(ziel + ".tmp")
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		var geschrieben int64
		for _, s := range d.Stuecke {
			r, ok := offen[s.Zip]
			if !ok {
				r, err = zip.OpenReader(filepath.Join(zipOrdner, s.Zip))
				if err != nil {
					out.Close()
					return nil, fmt.Errorf("ZIP nicht lesbar: %s", s.Zip)
				}
				offen[s.Zip] = r
			}
			eintrag := findeEintrag(r, s.Eintrag)
			if eintrag == nil {
				out.Close()
				return nil, fmt.Errorf("Eintrag %s fehlt in %s", s.Eintrag, s.Zip)
			}
			lesen, err := eintrag.Open()
			if err != nil {
				out.Close()
				return nil, err
			}
			hs := sha256.New()
			n, err := io.Copy(io.MultiWriter(out, h, hs), lesen)
			lesen.Close()
			if err != nil {
				out.Close()
				return nil, err
			}
			if n != s.Bytes || hex.EncodeToString(hs.Sum(nil)) != s.Sha256 {
				out.Close()
				return nil, fmt.Errorf("Stück %s stimmt nicht", s.Eintrag)
			}
			geschrieben += n
		}
		if err := out.Close(); err != nil {
			return nil, err
		}
		if geschrieben != d.Bytes || hex.EncodeToString(h.Sum(nil)) != d.Sha256 {
			os.Remove(ziel + ".tmp")
			return nil, fmt.Errorf("Datei %s stimmt nicht", d.Pfad)
		}
		if err := os.Rename(ziel+".tmp", ziel); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func findeEintrag(r *zip.ReadCloser, name string) *zip.File {
	for _, f := range r.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// ladeManifest liest das externe Manifest einer Paketversion
func ladeManifest(pfad string) (Manifest, error) {
	var m Manifest
	daten, err := os.ReadFile(pfad)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(daten, &m); err != nil {
		return m, fmt.Errorf("Manifest ungültig: %w", err)
	}
	return m, nil
}
