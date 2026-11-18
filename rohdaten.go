package main

// Rohdaten: was schon im Arbeitsordner liegt (OSM-Datei, Höhenkacheln). Der Ablauf zeigt Zeitpunkt und Größe an.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RohStand beschreibt vorhandene Rohdaten eines Schritts
type RohStand struct {
	Seit    string `json:"seit"`
	Groesse string `json:"groesse"`
	Detail  string `json:"detail,omitempty"`
}

// rohStand liefert den Stand der Rohdaten eines Schritts, nil wenn keine vorhanden sind
func rohStand(id, arbeit string) *RohStand {
	switch id {
	case "osm":
		info, err := os.Stat(filepath.Join(arbeit, "roh", "germany-latest.osm.pbf"))
		if err != nil {
			return nil
		}
		return &RohStand{Seit: zeitText(info.ModTime()), Groesse: groesseText(info.Size())}
	case "hoehen":
		var anzahl int
		var summe int64
		var neueste time.Time
		filepath.WalkDir(filepath.Join(arbeit, "hoehen"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".hgt.gz") {
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
		if anzahl == 0 {
			return nil
		}
		return &RohStand{Seit: zeitText(neueste), Groesse: groesseText(summe), Detail: fmt.Sprintf("%d Kacheln", anzahl)}
	}
	return nil
}

func zeitText(t time.Time) string { return t.Format("02.01.2006 15:04") }

// groesseText: GB mit zwei Nachkommastellen ab 1 GB, sonst MB (Zweierpotenzen)
func groesseText(n int64) string {
	if n >= 1<<30 {
		return strings.Replace(fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30)), ".", ",", 1)
	}
	return fmt.Sprintf("%d MB", n>>20)
}
