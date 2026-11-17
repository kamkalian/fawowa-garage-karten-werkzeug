package main

// Laden der Daten: Kartendaten (Geofabrik) und Höhenkacheln (SRTM). Eine Datei wird nur neu geladen, wenn sie fehlt oder der
// Anbieter eine jüngere Fassung hat (Last-Modified). Der Stand steht in <Datei>.stand.json.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type standDatei struct {
	Quelle       string `json:"quelle"`
	LastModified string `json:"last_modified"`
	GeladenAm    string `json:"geladen_am"`
}

// hoehenName: Name einer Höhenkachel wie im SRTM-Datensatz, z. B. N47E006
func hoehenName(lat, lon int) string {
	buch := "N"
	if lat < 0 {
		buch = "S"
	}
	ost := "E"
	if lon < 0 {
		ost = "W"
	}
	return fmt.Sprintf("%s%02d%s%03d", buch, absI(lat), ost, absI(lon))
}

func absI(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

var klient = &http.Client{Timeout: 0}

// ladeDatei lädt url nach ziel, wenn nötig. optional: 404 gilt als „es gibt hier keine Daten“ (liefert fehlt = true).
func ladeDatei(url, ziel string, optional, neu bool, zeigeName string, log func(string, ...any)) (geladen bool, fehlt bool, err error) {
	stand := ziel + ".stand.json"
	var alt *standDatei
	if daten, err := os.ReadFile(stand); err == nil {
		var s standDatei
		if json.Unmarshal(daten, &s) == nil {
			alt = &s
		}
	} else if info, err := os.Stat(ziel); err == nil {
		// Datei von einem früheren Lauf ohne Stand-Datei: das Änderungsdatum gilt als Stand
		alt = &standDatei{LastModified: info.ModTime().UTC().Format(http.TimeFormat)}
	}

	if _, err := os.Stat(ziel); err == nil && !neu && alt != nil {
		kopf, err := klient.Head(url)
		if err == nil {
			kopf.Body.Close()
			if kopf.StatusCode != http.StatusOK && kopf.StatusCode != http.StatusNotFound {
				return false, false, nil
			}
			if kopf.StatusCode == http.StatusOK {
				neuer := false
				if lm := kopf.Header.Get("Last-Modified"); lm != "" && alt.LastModified != "" {
					r, e1 := http.ParseTime(lm)
					a, e2 := http.ParseTime(alt.LastModified)
					neuer = e1 == nil && e2 == nil && r.After(a)
				}
				if !neuer {
					return false, false, nil
				}
			}
		}
	}

	antwort, err := klient.Get(url)
	if err != nil {
		return false, false, err
	}
	defer antwort.Body.Close()
	if antwort.StatusCode == http.StatusNotFound && optional {
		return false, true, nil
	}
	if antwort.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("Download fehlgeschlagen (HTTP %d): %s", antwort.StatusCode, url)
	}
	if err := os.MkdirAll(dirOf(ziel), 0o755); err != nil {
		return false, false, err
	}
	gesamt := antwort.ContentLength
	datei, err := os.Create(ziel + ".teil")
	if err != nil {
		return false, false, err
	}
	geladen64 := int64(0)
	letzte := time.Now()
	buf := make([]byte, 1<<20)
	for {
		n, rerr := antwort.Body.Read(buf)
		if n > 0 {
			if _, werr := datei.Write(buf[:n]); werr != nil {
				datei.Close()
				return false, false, werr
			}
			geladen64 += int64(n)
			if time.Since(letzte) > 500*time.Millisecond && gesamt > 0 {
				letzte = time.Now()
				fortschrittZeile(zeigeName, float64(geladen64)/float64(gesamt), fmt.Sprintf("%s von %s", mb(geladen64), mb(gesamt)))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			datei.Close()
			return false, false, rerr
		}
	}
	if err := datei.Close(); err != nil {
		return false, false, err
	}
	if err := os.Rename(ziel+".teil", ziel); err != nil {
		return false, false, err
	}
	s := standDatei{Quelle: url, LastModified: antwort.Header.Get("Last-Modified"), GeladenAm: time.Now().UTC().Format(time.RFC3339)}
	if daten, err := json.MarshalIndent(s, "", "  "); err == nil {
		os.WriteFile(stand, daten, 0o644)
	}
	if gesamt > 0 {
		fortschrittZeile(zeigeName, 1, mb(geladen64))
		fmt.Println()
	}
	return true, false, nil
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}
