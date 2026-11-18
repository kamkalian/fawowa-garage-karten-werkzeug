package main

// Betrieb ohne Oberfläche: daten-werkzeug.exe --ohne-oberflaeche --alles führt alle Schritte aus und meldet den Fortschritt
// im Terminal. Für die Aufgabenplanung von Windows gedacht.

import (
	"fmt"
	"os"
	"path/filepath"
)

func laeufOhneOberflaeche(alles bool) error {
	ex, _ := os.Executable()
	standard := filepath.Join(filepath.Dir(ex), "daten")
	einst, err := LadeEinstellungen(EinstellungenDatei(), standard)
	if err != nil {
		return err
	}
	hub := neuerHub()
	kanal := hub.Abonnieren()
	engine := neueEngine(hub, func() Einstellungen { return einst })
	var ids []string
	if alles {
		for _, s := range schrittListe {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("Keine Schritte gewählt (zum Beispiel: --alles)")
	}
	if err := engine.Start(ids); err != nil {
		return err
	}
	for e := range kanal {
		switch e.Art {
		case "schritt":
			fmt.Printf("%s  %-30s %s\n", e.Zeit, e.Schritt, e.Status)
		case "protokoll":
			fmt.Printf("%s  %s\n", e.Zeit, e.Text)
		case "fortschritt":
			fmt.Printf("%s  %-30s %3d %%  %s\n", e.Zeit, e.Schritt, int(e.Anteil*100), e.Text)
		case "fertig":
			fmt.Println("Ablauf beendet.")
			return nil
		}
	}
	return nil
}
