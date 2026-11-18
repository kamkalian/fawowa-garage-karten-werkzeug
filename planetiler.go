package main

// Kartenbau mit Planetiler: erzeugt aus der OSM-Datei eine PMTiles-Datei (Vektorkacheln). Der Prozess läuft außerhalb des
// Werkzeugs; seine Ausgabe geht ins Protokoll, ein Abbruch beendet den Prozess.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// planetilerArgumente stellt die Befehlszeile zusammen (ohne Java selbst)
func planetilerArgumente(jar, pbf, ausgabe, quellen string, xmxGB, maxZoom int, mmap bool) []string {
	args := []string{
		fmt.Sprintf("-Xmx%dg", xmxGB),
		"-jar", jar,
		"--osm-path=" + pbf,
		"--output=" + ausgabe,
		"--download",
		"--download-dir=" + quellen,
		fmt.Sprintf("--maxzoom=%d", maxZoom),
		"--force",
	}
	if mmap {
		args = append(args, "--storage=mmap")
	}
	return args
}

// planetilerLauf startet Planetiler und gibt jede Ausgabezeile an logf weiter
func planetilerLauf(ctx context.Context, java, jar, pbf, karteOrdner, quellen string, xmxGB, maxZoom int, mmap bool, logf func(string, ...any)) error {
	if err := os.MkdirAll(karteOrdner, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(quellen, 0o755); err != nil {
		return err
	}
	ausgabe := filepath.Join(karteOrdner, "germany.pmtiles")
	// Eine unvollständige Datei vom letzten Versuch wegräumen
	os.Remove(ausgabe + ".tmp")
	cmd := exec.CommandContext(ctx, java, planetilerArgumente(jar, pbf, ausgabe, quellen, xmxGB, maxZoom, mmap)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	fehlerAus, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Planetiler nicht startbar: %w", err)
	}
	var wg sync.WaitGroup
	lesen := func(r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64*1024), 1024*1024)
		for s.Scan() {
			zeile := strings.TrimSpace(s.Text())
			if zeile != "" {
				logf("%s", zeile)
			}
		}
	}
	wg.Add(2)
	go lesen(out)
	go lesen(fehlerAus)
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Kartenbau abgebrochen")
		}
		return fmt.Errorf("Planetiler endete mit Fehler: %w", err)
	}
	if _, err := os.Stat(ausgabe); err != nil {
		return fmt.Errorf("Planetiler hat keine Karte erzeugt")
	}
	logf("Karte fertig: %s", ausgabe)
	return nil
}
