package main

// Einstellungen des Werkzeugs: %APPDATA%\fawowa-daten\einstellungen.json. Die Passphrase der Schlüsseldatei steht nur
// verschlüsselt (DPAPI) in der Datei; in Antworten an die Oberfläche erscheint sie nie, nur die Angabe „gesetzt“.
// Ein Passwort für die Anmeldung gibt es nicht: angemeldet wird mit dem Schlüssel.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type ServerEinstellungen struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Benutzer          string `json:"benutzer"`
	Schluessel        string `json:"schluessel"`
	ZielOrdner        string `json:"zielOrdner"`
	PassphraseVerschl string `json:"passphraseVerschl,omitempty"`
	PassphraseMerken  bool   `json:"passphraseMerken"`
	KnownHosts        string `json:"knownHosts"`
}

type Einstellungen struct {
	Arbeitsordner   string              `json:"arbeitsordner"`
	MaxZipMB        int                 `json:"maxZipMB"`
	PlanetilerXmxGB int                 `json:"planetilerXmxGB"`
	PlanetilerZoom  int                 `json:"planetilerZoom"`
	PlanetilerMmap  bool                `json:"planetilerMmap"`
	Server          ServerEinstellungen `json:"server"`
}

// StandardEinstellungen: Werte, die man als Ausgangspunkt braucht
func StandardEinstellungen(arbeitsordner string) Einstellungen {
	return Einstellungen{
		Arbeitsordner:   arbeitsordner,
		MaxZipMB:        200,
		PlanetilerXmxGB: 8,
		PlanetilerZoom:  14,
		Server: ServerEinstellungen{
			Port:       22,
			ZielOrdner: "/srv/apps/garage/data/import",
		},
	}
}

// EinstellungenDatei: Pfad der Einstellungsdatei (unter %APPDATA%, sonst neben der exe)
func EinstellungenDatei() string {
	basis, err := os.UserConfigDir()
	if err != nil {
		basis = "."
	}
	return filepath.Join(basis, "fawowa-daten", "einstellungen.json")
}

// LadeEinstellungen liest die Datei; fehlt sie, kommen die Standardwerte
func LadeEinstellungen(pfad, standardOrdner string) (Einstellungen, error) {
	e := StandardEinstellungen(standardOrdner)
	daten, err := os.ReadFile(pfad)
	if errors.Is(err, os.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(daten, &e); err != nil {
		return e, err
	}
	return e, nil
}

// SpeichereEinstellungen schreibt die Datei (Geheimnisse sind schon verschlüsselt übergeben)
func SpeichereEinstellungen(pfad string, e Einstellungen) error {
	if err := os.MkdirAll(filepath.Dir(pfad), 0o700); err != nil {
		return err
	}
	daten, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := pfad + ".tmp"
	if err := os.WriteFile(tmp, daten, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, pfad)
}

// Oeffentlich ist die Sicht für die Oberfläche: ohne Geheimnisse, nur mit der Angabe, ob die Passphrase gesetzt ist
type Oeffentlich struct {
	Einstellungen
	PassphraseGesetzt bool `json:"passphraseGesetzt"`
}

func OeffentlichSicht(e Einstellungen) Oeffentlich {
	gesetzt := Oeffentlich{PassphraseGesetzt: e.Server.PassphraseVerschl != ""}
	e.Server.PassphraseVerschl = ""
	gesetzt.Einstellungen = e
	return gesetzt
}
