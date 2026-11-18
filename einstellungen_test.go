package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEinstellungenSpeichernOhneKlartextGeheimnis(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "einstellungen.json")
	e := StandardEinstellungen("C:/daten")
	e.Server.Host = "server.example"
	e.Server.PassphraseVerschl = "VERSCHLUESSELT-BASE64=="
	if err := SpeichereEinstellungen(pfad, e); err != nil {
		t.Fatal(err)
	}
	roh, _ := os.ReadFile(pfad)
	if !strings.Contains(string(roh), "VERSCHLUESSELT-BASE64==") {
		t.Fatal("verschlüsseltes Geheimnis fehlt in der Datei")
	}
	geladen, err := LadeEinstellungen(pfad, "C:/daten")
	if err != nil {
		t.Fatal(err)
	}
	if geladen.Server.Host != "server.example" || geladen.MaxZipMB != 200 {
		t.Fatalf("falsch geladen: %+v", geladen)
	}
}

func TestEinstellungenOeffentlichZeigtKeinGeheimnis(t *testing.T) {
	e := StandardEinstellungen("x")
	e.Server.PassphraseVerschl = "geheim-verschl"
	sicht := OeffentlichSicht(e)
	if !sicht.PassphraseGesetzt {
		t.Fatalf("Flag falsch: %+v", sicht)
	}
	if sicht.Server.PassphraseVerschl != "" {
		t.Fatal("das Geheimnis taucht in der Oberflächen-Sicht auf")
	}
}

func TestEinstellungenFehlendeDateiGibtStandard(t *testing.T) {
	e, err := LadeEinstellungen(filepath.Join(t.TempDir(), "gibt-es-nicht.json"), "D:/x")
	if err != nil {
		t.Fatal(err)
	}
	if e.Arbeitsordner != "D:/x" || e.PlanetilerZoom != 14 {
		t.Fatalf("Standard falsch: %+v", e)
	}
}
