package main

// Daten je Schritt: was schon auf der Platte liegt, mit Zeitpunkt, Größe und Beschreibung. Der Ablauf zeigt es an, damit
// man sieht, was fehlt und was neu geladen werden soll.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DatenStand: Stand der Daten eines Schritts. Vorhanden = die Daten liegen da.
type DatenStand struct {
	Vorhanden bool   `json:"vorhanden"`
	Seit      string `json:"seit,omitempty"`
	Groesse   string `json:"groesse,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// datenStand liefert den Stand der Daten eines Schritts (immer, auch wenn nichts da ist)
func datenStand(id, arbeit string) DatenStand {
	switch id {
	case "osm":
		info, err := os.Stat(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"))
		if err != nil {
			return DatenStand{Detail: "nicht geladen"}
		}
		return DatenStand{Vorhanden: true, Seit: zeitText(info.ModTime()), Groesse: groesseText(info.Size()), Detail: "OSM-Datei Deutschland"}
	case "hoehen":
		anzahl, summe, neueste := ordnerStand(filepath.Join(arbeit, "hoehen"), ".hgt.gz")
		if anzahl == 0 {
			return DatenStand{Detail: "nicht geladen"}
		}
		return DatenStand{Vorhanden: true, Seit: zeitText(neueste), Groesse: groesseText(summe), Detail: fmt.Sprintf("%d Kacheln", anzahl)}
	case "netz":
		info, err := os.Stat(filepath.Join(arbeit, "netz", "info.json"))
		if err != nil {
			return DatenStand{Detail: "nicht gebaut"}
		}
		_, summe, _ := ordnerStand(filepath.Join(arbeit, "netz"), "")
		return DatenStand{Vorhanden: true, Seit: zeitText(info.ModTime()), Groesse: groesseText(summe), Detail: "Wegenetz und Ortsdaten"}
	case "karte-werkzeuge":
		info, err := os.Stat(filepath.Join(arbeit, "werkzeuge", "planetiler-"+planetilerVersion+".jar"))
		if err != nil || findeJava(filepath.Join(arbeit, "werkzeuge", "jre")) == "" {
			return DatenStand{Detail: "nicht bereit"}
		}
		return DatenStand{Vorhanden: true, Seit: zeitText(info.ModTime()), Detail: "Java und Planetiler " + planetilerVersion}
	case "karte":
		info, err := os.Stat(filepath.Join(arbeit, "karte", "germany.pmtiles"))
		if err != nil {
			return DatenStand{Detail: "nicht gebaut"}
		}
		return DatenStand{Vorhanden: true, Seit: zeitText(info.ModTime()), Groesse: groesseText(info.Size()), Detail: "PMTiles-Karte"}
	case "paket-netz", "paket-karte":
		praefix := "netz-"
		if id == "paket-karte" {
			praefix = "karte-"
		}
		name := neuesteUnter(filepath.Join(arbeit, "pakete"), praefix)
		if name == "" {
			return DatenStand{Detail: "noch nicht gepackt"}
		}
		pfad := filepath.Join(arbeit, "pakete", name)
		info, err := os.Stat(pfad)
		if err != nil {
			return DatenStand{Detail: "nicht gepackt"}
		}
		_, summe, _ := ordnerStand(pfad, "")
		return DatenStand{Vorhanden: true, Seit: zeitText(info.ModTime()), Groesse: groesseText(summe), Detail: name}
	case "hochladen":
		return hochladenStand(arbeit)
	}
	return DatenStand{Detail: "noch nicht umgesetzt"}
}

// hochladenStand: welche Pakete gepackt sind und welche schon auf dem Server angekommen sind
func hochladenStand(arbeit string) DatenStand {
	var lokal []string
	for _, praefix := range []string{"netz-", "karte-"} {
		if n := neuesteUnter(filepath.Join(arbeit, "pakete"), praefix); n != "" {
			lokal = append(lokal, n)
		}
	}
	if len(lokal) == 0 {
		return DatenStand{Detail: "keine Pakete gepackt"}
	}
	hoch := schrittStandLesen(arbeit, "hochladen").Hochgeladen
	var fehlt, da []string
	for _, n := range lokal {
		if zeit, ok := hoch[n]; ok {
			da = append(da, n+" (am "+zeit+")")
		} else {
			fehlt = append(fehlt, n)
		}
	}
	var teile []string
	if len(fehlt) > 0 {
		teile = append(teile, "noch nicht hochgeladen: "+strings.Join(fehlt, ", "))
	}
	if len(da) > 0 {
		teile = append(teile, "auf dem Server: "+strings.Join(da, ", "))
	}
	return DatenStand{Vorhanden: true, Detail: strings.Join(teile, " · ")}
}

// ordnerStand zählt die Dateien unterhalb eines Ordners (mit der Endung, leer = alle), deren Summe und die jüngste Zeit
func ordnerStand(ordner, endung string) (anzahl int, summe int64, neueste time.Time) {
	filepath.WalkDir(ordner, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (endung != "" && !strings.HasSuffix(p, endung)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		anzahl++
		summe += info.Size()
		if info.ModTime().After(neueste) {
			neueste = info.ModTime()
		}
		return nil
	})
	return
}

// neuesteUnter: der Name des jüngsten Eintrags mit dem Präfix (Ordnernamen enthalten den Zeitstempel), leer wenn keiner da ist
func neuesteUnter(ordner, praefix string) string {
	eintraege, err := os.ReadDir(ordner)
	if err != nil {
		return ""
	}
	var namen []string
	for _, e := range eintraege {
		if strings.HasPrefix(e.Name(), praefix) && !strings.HasSuffix(e.Name(), ".hochladen") {
			namen = append(namen, e.Name())
		}
	}
	if len(namen) == 0 {
		return ""
	}
	sort.Strings(namen)
	return namen[len(namen)-1]
}

func zeitText(t time.Time) string { return t.Format("02.01.2006 15:04") }

// groesseText: GB mit zwei Nachkommastellen ab 1 GB, sonst MB (Zweierpotenzen)
func groesseText(n int64) string {
	if n >= 1<<30 {
		return strings.Replace(fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30)), ".", ",", 1)
	}
	return fmt.Sprintf("%d MB", n>>20)
}
