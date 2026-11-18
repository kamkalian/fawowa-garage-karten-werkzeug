package main

// Werkzeuge für die Karte: Java-Laufzeit (Temurin 21, falls keine vorhanden) und Planetiler in fester Version. Beide Downloads
// werden mit einer SHA-256-Prüfsumme geprüft; stimmt sie nicht, wird die Datei gelöscht und der Schritt bricht ab.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	planetilerVersion = "0.10.2"
	planetilerUrl     = "https://github.com/onthegomap/planetiler/releases/download/v0.10.2/planetiler.jar"
	// Veröffentlichte SHA-256-Prüfsumme der Datei planetiler.jar (v0.10.2)
	planetilerSha256 = "f310bd0413e2e4512b27f4046d418664e8e1d3bf31603c2a70e23de06c167e4d"
	adoptiumAssets   = "https://api.adoptium.net/v3/assets/latest/21/hotspot?architecture=x64&image_type=jre&os=windows&vendor=eclipse"
)

// dateiSha256 berechnet die SHA-256-Prüfsumme einer Datei
func dateiSha256(pfad string) (string, error) {
	f, err := os.Open(pfad)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ladeMitPruefsumme lädt eine Datei und prüft die Prüfsumme; bei Abweichung wird die Datei gelöscht
func ladeMitPruefsumme(url, ziel, erwartet string, logf func(string, ...any)) error {
	if _, err := os.Stat(ziel); err == nil {
		if sum, err := dateiSha256(ziel); err == nil && sum == erwartet {
			return nil
		}
		os.Remove(ziel)
	}
	if _, _, err := ladeDatei(url, ziel, false, true, filepath.Base(ziel), logf); err != nil {
		return err
	}
	sum, err := dateiSha256(ziel)
	if err != nil {
		return err
	}
	if sum != erwartet {
		os.Remove(ziel)
		return fmt.Errorf("Prüfsumme von %s stimmt nicht (erwartet %s, bekommen %s)", filepath.Base(ziel), erwartet, sum)
	}
	return nil
}

// javaPfad sucht eine vorhandene Java-Laufzeit: JAVA_HOME, dann java im PATH
func javaPfad() string {
	if home := os.Getenv("JAVA_HOME"); home != "" {
		p := filepath.Join(home, "bin", "java.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		return p
	}
	return ""
}

// javaVersion prüft, ob die Laufzeit mindestens Java 21 ist
func javaVersion(java string) bool {
	out, err := exec.Command(java, "-version").CombinedOutput()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "version \"21") || strings.Contains(s, "version \"22") || strings.Contains(s, "version \"23") ||
		strings.Contains(s, "version \"24") || strings.Contains(s, "version \"25") || strings.Contains(s, "version \"26")
}

// sichereJava liefert den Pfad zu java.exe; lädt Temurin 21 JRE nach <werkzeuge>/jre, wenn nötig
func sichereJava(werkzeuge string, logf func(string, ...any)) (string, error) {
	if j := javaPfad(); j != "" && javaVersion(j) {
		logf("Java vorhanden: %s", j)
		return j, nil
	}
	ziel := filepath.Join(werkzeuge, "jre")
	if j := findeJava(ziel); j != "" {
		logf("Java aus früherem Lauf: %s", j)
		return j, nil
	}
	// Angaben (Adresse und Prüfsumme) von Adoptium
	antwort, err := http.Get(adoptiumAssets)
	if err != nil {
		return "", fmt.Errorf("Java-Angaben nicht erreichbar: %w", err)
	}
	defer antwort.Body.Close()
	var assets []struct {
		Binary struct {
			Package struct {
				Link     string `json:"link"`
				Checksum string `json:"checksum"`
				Name     string `json:"name"`
			} `json:"package"`
		} `json:"binary"`
	}
	if err := json.NewDecoder(antwort.Body).Decode(&assets); err != nil || len(assets) == 0 {
		return "", fmt.Errorf("Java-Angaben ungültig")
	}
	paket := assets[0].Binary.Package
	zipDatei := filepath.Join(werkzeuge, paket.Name)
	logf("Lade Java-Laufzeit (Temurin 21, %s)", paket.Name)
	if err := ladeMitPruefsumme(paket.Link, zipDatei, paket.Checksum, logf); err != nil {
		return "", err
	}
	if err := entpackeZip(zipDatei, ziel); err != nil {
		return "", err
	}
	os.Remove(zipDatei)
	j := findeJava(ziel)
	if j == "" {
		return "", fmt.Errorf("java.exe nach dem Entpacken nicht gefunden")
	}
	return j, nil
}

// findeJava sucht bin/java.exe unterhalb eines Ordners
func findeJava(ordner string) string {
	var gefunden string
	filepath.Walk(ordner, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.EqualFold(info.Name(), "java.exe") && gefunden == "" {
			gefunden = p
		}
		return nil
	})
	return gefunden
}

// entpackeZip entpackt eine ZIP (mit Schutz gegen Pfade außerhalb des Zielordners)
func entpackeZip(zipDatei, ziel string) error {
	r, err := zip.OpenReader(zipDatei)
	if err != nil {
		return err
	}
	defer r.Close()
	wurzel := filepath.Clean(ziel) + string(os.PathSeparator)
	for _, f := range r.File {
		pfad := filepath.Join(ziel, f.Name)
		if !strings.HasPrefix(pfad, wurzel) {
			return fmt.Errorf("ungültiger Pfad in der ZIP: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(pfad, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(pfad), 0o755)
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(pfad)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// sicherePlanetiler lädt planetiler.jar in fester Version und prüft die Prüfsumme
func sichernPlanetiler(werkzeuge string, logf func(string, ...any)) (string, error) {
	ziel := filepath.Join(werkzeuge, "planetiler-"+planetilerVersion+".jar")
	logf("Planetiler %s", planetilerVersion)
	if err := ladeMitPruefsumme(planetilerUrl, ziel, planetilerSha256, logf); err != nil {
		return "", err
	}
	return ziel, nil
}

// sichereWerkzeuge: Java und Planetiler bereitstellen
func sichereWerkzeuge(werkzeuge string, logf func(string, ...any)) (java, jar string, err error) {
	if err := os.MkdirAll(werkzeuge, 0o755); err != nil {
		return "", "", err
	}
	java, err = sichereJava(werkzeuge, logf)
	if err != nil {
		return "", "", err
	}
	jar, err = sichernPlanetiler(werkzeuge, logf)
	return java, jar, err
}

// sha256Text liefert die Prüfsumme als Text (für Tests und Protokoll)
func sha256Text(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
