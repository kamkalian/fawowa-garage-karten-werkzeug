// Baut die Karten- und Wegedaten für den Reichweiten-Planer und packt sie in eine ZIP-Datei.
// Die ZIP-Datei lädst du danach in der Verwaltung hoch (Reiter „Kartendaten“).
//
// Start:  daten-bauen.exe            (im Ordner, in dem der Arbeitsordner „daten“ entstehen soll)
// Optionen:
//
//	-ordner <Pfad>   Arbeitsordner (Standard: daten)
//	-neu             Karte und Wegenetz neu bauen; vorhandene Downloads werden nur bei neueren Daten erneuert
//
// Ablauf: 1 Kartendaten Deutschland  2 Höhendaten  3 Wegenetz  4 Karte  5 ZIP-Datei.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const kartenUrl = "https://download.geofabrik.de/europe/germany-latest.osm.pbf"
const hoehenUrl = "https://elevation-tiles-prod.s3.amazonaws.com/skadi"

var (
	hoehenLat = [2]int{47, 55}
	hoehenLon = [2]int{5, 15}
)

var start = time.Now()

func main() {
	// Ohne Argumente: Oberfläche im Browser
	if len(os.Args) == 1 {
		ex, _ := os.Executable()
		if err := StarteOberflaeche(filepath.Join(filepath.Dir(ex), "daten"), uiDateien); err != nil {
			fehler(err)
		}
		return
	}
	// Unterbefehl für den Server: daten-bauen karte <pbf> <netzordner>
	if len(os.Args) == 4 && os.Args[1] == "karte" {
		fortschrittMelden = func(p int) { fmt.Printf("FORTSCHRITT %d\n", p) }
		if err := karteBauen(os.Args[2], os.Args[3], func(f string, a ...any) { fmt.Printf(f+"\n", a...) }); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ordnerArg := flag.String("ordner", "daten", "Arbeitsordner")
	neu := flag.Bool("neu", false, "Karte und Wegenetz neu bauen")
	flag.Parse()

	arbeit, err := filepath.Abs(*ordnerArg)
	if err != nil {
		fehler(err)
	}
	roh := filepath.Join(arbeit, "roh")
	hoehen := filepath.Join(arbeit, "hoehen")
	netz := filepath.Join(arbeit, "netz")
	pbf := filepath.Join(roh, "germany-latest.osm.pbf")

	fmt.Println("FaWoWa-Garage: Karten- und Wegedaten bauen")
	fmt.Println("Arbeitsordner:", arbeit)
	if *neu {
		fmt.Println("(neu bauen)")
	}

	// 1. Kartendaten
	schritt(1, "Kartendaten Deutschland laden (OpenStreetMap, ca. 5 GB)")
	geladen, _, err := ladeDatei(kartenUrl, pbf, false, *neu, "Kartendaten", nil)
	if err != nil {
		fehler(err)
	}
	if geladen {
		fmt.Println("  fertig.")
	} else {
		fmt.Println("  schon vorhanden und aktuell.")
	}

	// 2. Höhendaten
	schritt(2, "Höhendaten laden (SRTM, Deutschland)")
	var namen []string
	for lat := hoehenLat[0]; lat <= hoehenLat[1]; lat++ {
		for lon := hoehenLon[0]; lon <= hoehenLon[1]; lon++ {
			namen = append(namen, hoehenName(lat, lon))
		}
	}
	neuGeladen := 0
	for i, name := range namen {
		url := hoehenUrl + "/" + name[:3] + "/" + name + ".hgt.gz"
		ok, _, err := ladeDatei(url, filepath.Join(hoehen, name+".hgt.gz"), true, *neu, "", nil)
		if err != nil {
			fehler(err)
		}
		if ok {
			neuGeladen++
		}
		fortschrittZeile(fmt.Sprintf("Kachel %d von %d", i+1, len(namen)), float64(i+1)/float64(len(namen)), "")
	}
	fmt.Printf("\n  %d Kacheln neu geladen.\n", neuGeladen)

	// 3. Wegenetz
	schritt(3, "Wegenetz bauen (dauert länger, braucht viel Arbeitsspeicher)")
	if err := netzBauen(pbf, hoehen, netz, logZeile); err != nil {
		fehler(err)
	}

	// 4. Karte
	schritt(4, "Karte bauen")
	if err := karteBauen(pbf, netz, logZeile); err != nil {
		fehler(err)
	}

	// 5. ZIP-Datei
	schritt(5, "ZIP-Datei packen")
	datum := time.Now().Format("2006-01-02")
	zipDatei := filepath.Join(arbeit, "garage-netz-"+datum+".zip")
	if err := zippen(netz, zipDatei); err != nil {
		fehler(err)
	}
	info, err := os.Stat(zipDatei)
	if err != nil {
		fehler(err)
	}
	fmt.Printf("\nFertig nach %s.\n", dauer(time.Since(start)))
	fmt.Printf("Datei: %s (%s)\n", zipDatei, mb(info.Size()))
	fmt.Println("Hochladen: Verwaltung → Reiter „Kartendaten“ → „ZIP hochladen“.")
}

// schritt zeigt den Beginn eines Arbeitsschritts
func schritt(nr int, text string) {
	fmt.Printf("\n[%d/5] %s\n", nr, text)
}

// logZeile gibt Meldungen der Bauschritte aus (mit verstrichener Zeit)
func logZeile(format string, a ...any) {
	fmt.Printf("  %s  "+format+"\n", append([]any{dauer(time.Since(start))}, a...)...)
}

// fortschrittZeile überschreibt die aktuelle Terminalzeile mit einem Balken
// fortschrittHook leitet den Fortschritt an die Oberfläche weiter (nil: Anzeige im Terminal)
var fortschrittHook func(name string, anteil float64, text string)

func fortschrittZeile(name string, anteil float64, text string) {
	if fortschrittHook != nil {
		fortschrittHook(name, anteil, text)
		return
	}
	if anteil < 0 {
		anteil = 0
	}
	if anteil > 1 {
		anteil = 1
	}
	const breite = 30
	n := int(anteil * breite)
	balken := strings.Repeat("#", n) + strings.Repeat("-", breite-n)
	if text != "" {
		text = "  " + text
	}
	fmt.Printf("\r  %s [%s] %3d %%%s", name, balken, int(anteil*100), text)
}

// dauer formatiert eine Dauer als m:ss
func dauer(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d min", s/60, s%60)
}

// mb formatiert Bytes in Megabyte
func mb(n int64) string {
	return fmt.Sprintf("%d MB", n/1048576)
}

func fehler(err error) {
	fmt.Fprintf(os.Stderr, "\nFehler: %v\n", err)
	warteAufEnter()
	os.Exit(1)
}

// warteAufEnter hält das Fenster offen, wenn das Programm per Doppelklick startet
func warteAufEnter() {
	fmt.Print("\nEnter drücken zum Beenden ...")
	var s string
	fmt.Scanln(&s)
}
