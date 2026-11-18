package main

// Stand der Schritte auf der Platte: wann der letzte Lauf war, wie er ausging und dauerte. Für den Upload zusätzlich,
// welche Pakete schon auf dem Server sind. Liegt in <Arbeitsordner>/stand/<Schritt>.json, damit der Stand überlebt,
// wenn die Seite neu geladen wird oder das Werkzeug neu startet.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// LetzterLauf: Ergebnis des letzten Laufs eines Schritts
type LetzterLauf struct {
	Zeit     string `json:"zeit"`
	Ergebnis string `json:"ergebnis"` // fertig | fehler | ausstehend | abgebrochen
	Meldung  string `json:"meldung,omitempty"`
	DauerSek int    `json:"dauerSek"`
}

// SchrittStand: Gespeichertes eines Schritts
type SchrittStand struct {
	Letzter     *LetzterLauf      `json:"letzter,omitempty"`
	Hochgeladen map[string]string `json:"hochgeladen,omitempty"` // Paketname -> Zeitpunkt des Uploads
}

func schrittStandPfad(arbeit, id string) string { return filepath.Join(arbeit, "stand", id+".json") }

// schrittStandLesen liefert den gespeicherten Stand; fehlt die Datei oder ist sie kaputt, einen leeren
func schrittStandLesen(arbeit, id string) SchrittStand {
	var s SchrittStand
	if daten, err := os.ReadFile(schrittStandPfad(arbeit, id)); err == nil {
		json.Unmarshal(daten, &s)
	}
	return s
}

func schrittStandSchreiben(arbeit, id string, s SchrittStand) error {
	pfad := schrittStandPfad(arbeit, id)
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		return err
	}
	daten, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := pfad + ".tmp"
	if err := os.WriteFile(tmp, daten, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, pfad)
}

// letzterLaufNotieren hält fest, wie der Schritt gelaufen ist
func letzterLaufNotieren(arbeit, id string, start time.Time, ergebnis, meldung string) {
	s := schrittStandLesen(arbeit, id)
	s.Letzter = &LetzterLauf{Zeit: zeitText(time.Now()), Ergebnis: ergebnis, Meldung: meldung, DauerSek: int(time.Since(start).Seconds())}
	schrittStandSchreiben(arbeit, id, s)
}

// paketHochgeladenNotieren merkt sich, dass ein Paket auf dem Server angekommen ist
func paketHochgeladenNotieren(arbeit, paket string) {
	s := schrittStandLesen(arbeit, "hochladen")
	if s.Hochgeladen == nil {
		s.Hochgeladen = map[string]string{}
	}
	s.Hochgeladen[paket] = zeitText(time.Now())
	schrittStandSchreiben(arbeit, "hochladen", s)
}
