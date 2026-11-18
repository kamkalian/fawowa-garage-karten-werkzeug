package main

// Ablauf der Schritte für die Oberfläche: Schritte nacheinander ausführen, Status und Fortschritt als Ereignisse melden,
// Abbruch zwischen den Schritten. Die Schritte selbst (Download, Höhen, Wegenetz, Packen, Hochladen) sind die Bausteine
// aus den anderen Dateien.

import (
	"context"
	"fmt"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Schritt-Kennungen und Namen (Reihenfolge wie im Plan)
var schrittListe = []struct{ ID, Name string }{
	{"osm", "OSM-Daten Deutschland laden"},
	{"hoehen", "Höhendaten laden"},
	{"netz", "Wegenetz bauen"},
	{"karte-werkzeuge", "Werkzeuge für die Karte (Java, Planetiler)"},
	{"karte", "Kacheln (PMTiles) bauen"},
	{"schriften", "Schriften und Symbole"},
	{"paket-netz", "Paket Wegenetz packen"},
	{"paket-karte", "Paket Karte packen"},
	{"hochladen", "Pakete auf den Server hochladen"},
}

// Ereignis: Statusmeldung für die Oberfläche (SSE)
type Ereignis struct {
	Art     string  `json:"art"` // schritt | fortschritt | protokoll | fertig
	Schritt string  `json:"schritt,omitempty"`
	Status  string  `json:"status,omitempty"` // offen | laeuft | fertig | fehler | ausstehend | uebersprungen
	Anteil  float64 `json:"anteil,omitempty"` // 0 bis 1
	Text    string  `json:"text,omitempty"`
	Zeit    string  `json:"zeit"`
}

// Hub verteilt Ereignisse an alle offenen Verbindungen
type Hub struct {
	mu      sync.Mutex
	kanaele map[chan Ereignis]bool
	letzte  []Ereignis // für Verbindungen, die später dazukommen
}

func neuerHub() *Hub { return &Hub{kanaele: map[chan Ereignis]bool{}} }

func (h *Hub) Abonnieren() chan Ereignis {
	k := make(chan Ereignis, 256)
	h.mu.Lock()
	h.kanaele[k] = true
	h.mu.Unlock()
	return k
}

func (h *Hub) Beenden(k chan Ereignis) {
	h.mu.Lock()
	delete(h.kanaele, k)
	h.mu.Unlock()
}

func (h *Hub) Senden(e Ereignis) {
	e.Zeit = time.Now().Format("15:04:05")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.letzte = append(h.letzte, e)
	if len(h.letzte) > 500 {
		h.letzte = h.letzte[len(h.letzte)-500:]
	}
	for k := range h.kanaele {
		select {
		case k <- e:
		default: // langsame Verbindung verliert Zwischenstände, bekommt den nächsten Stand
		}
	}
}

// Engine führt die gewählten Schritte aus
type Engine struct {
	mu      sync.Mutex
	hub     *Hub
	laeuft  bool
	abbruch context.CancelFunc
	status  map[string]string
	einst   func() Einstellungen
	java    string
	jar     string
}

func neueEngine(hub *Hub, einst func() Einstellungen) *Engine {
	e := &Engine{hub: hub, status: map[string]string{}, einst: einst}
	for _, s := range schrittListe {
		e.status[s.ID] = "offen"
	}
	return e
}

// StatusListe liefert Schritte mit aktuellem Status (für die Oberfläche)
func (e *Engine) StatusListe() []map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []map[string]string
	for _, s := range schrittListe {
		out = append(out, map[string]string{"id": s.ID, "name": s.Name, "status": e.status[s.ID]})
	}
	return out
}

func (e *Engine) Laeuft() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.laeuft
}

// Start startet die gewählten Schritte (in der Reihenfolge der Liste). Läuft schon etwas, passiert nichts.
func (e *Engine) Start(gewaehlt []string) error {
	e.mu.Lock()
	if e.laeuft {
		e.mu.Unlock()
		return fmt.Errorf("Es läuft schon ein Ablauf.")
	}
	ctx, abbruch := context.WithCancel(context.Background())
	e.laeuft = true
	e.abbruch = abbruch
	e.mu.Unlock()

	ids := map[string]bool{}
	for _, id := range gewaehlt {
		ids[id] = true
	}
	go func() {
		defer func() {
			e.mu.Lock()
			e.laeuft = false
			e.abbruch = nil
			e.mu.Unlock()
			e.hub.Senden(Ereignis{Art: "fertig"})
		}()
		for _, s := range schrittListe {
			if !ids[s.ID] {
				continue
			}
			if ctx.Err() != nil {
				e.setze(s.ID, "offen")
				e.protokoll("Abgebrochen.")
				return
			}
			e.setze(s.ID, "laeuft")
			err := e.fuehreAus(ctx, s.ID)
			switch {
			case err == errAusstehend:
				e.setze(s.ID, "ausstehend")
			case err != nil:
				e.setze(s.ID, "fehler")
				e.protokoll("Fehler in „" + s.Name + "“: " + err.Error())
				return
			default:
				e.setze(s.ID, "fertig")
			}
		}
	}()
	return nil
}

// Abbrechen beendet den Ablauf nach dem laufenden Schritt
func (e *Engine) Abbrechen() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.abbruch != nil {
		e.abbruch()
	}
}

func (e *Engine) setze(id, status string) {
	e.mu.Lock()
	e.status[id] = status
	e.mu.Unlock()
	e.hub.Senden(Ereignis{Art: "schritt", Schritt: id, Status: status})
}

func (e *Engine) protokoll(text string) {
	e.hub.Senden(Ereignis{Art: "protokoll", Text: text})
	// Zusätzlich ins Protokoll im Arbeitsordner: <arbeit>/protokoll/<datum>.log
	arbeit, _ := filepath.Abs(e.einst().Arbeitsordner)
	ordner := filepath.Join(arbeit, "protokoll")
	if os.MkdirAll(ordner, 0o755) == nil {
		datei := filepath.Join(ordner, time.Now().Format("2006-01-02")+".log")
		if f, err := os.OpenFile(datei, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s  %s\n", time.Now().Format("15:04:05"), text)
			f.Close()
		}
	}
}

// errAusstehend: der Schritt ist noch nicht umgesetzt (wird als „ausstehend“ angezeigt, der Ablauf geht weiter)
var errAusstehend = fmt.Errorf("noch nicht umgesetzt")

func (e *Engine) fuehreAus(ctx context.Context, id string) error {
	ein := e.einst()
	arbeit, _ := filepath.Abs(ein.Arbeitsordner)
	roh := filepath.Join(arbeit, "roh")
	hoehen := filepath.Join(arbeit, "hoehen")
	netz := filepath.Join(arbeit, "netz")
	pakete := filepath.Join(arbeit, "pakete")
	pbf := filepath.Join(roh, "germany-latest.osm.pbf")
	logf := func(format string, a ...any) { e.protokoll(fmt.Sprintf(format, a...)) }

	switch id {
	case "osm":
		fortschrittHook = func(name string, anteil float64, text string) {
			e.hub.Senden(Ereignis{Art: "fortschritt", Schritt: id, Anteil: anteil, Text: text})
		}
		defer func() { fortschrittHook = nil }()
		_, _, err := ladeDatei(kartenUrl, pbf, false, false, "OSM", logf)
		return err
	case "hoehen":
		fortschrittHook = func(name string, anteil float64, text string) {}
		defer func() { fortschrittHook = nil }()
		var namen []string
		for lat := hoehenLat[0]; lat <= hoehenLat[1]; lat++ {
			for lon := hoehenLon[0]; lon <= hoehenLon[1]; lon++ {
				namen = append(namen, hoehenName(lat, lon))
			}
		}
		for i, n := range namen {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			url := hoehenUrl + "/" + n[:3] + "/" + n + ".hgt.gz"
			if _, _, err := ladeDatei(url, filepath.Join(hoehen, n+".hgt.gz"), true, false, "", logf); err != nil {
				return err
			}
			e.hub.Senden(Ereignis{Art: "fortschritt", Schritt: id, Anteil: float64(i+1) / float64(len(namen)), Text: n})
		}
		return nil
	case "netz":
		if _, err := os.Stat(pbf); err != nil {
			return fmt.Errorf("OSM-Daten fehlen. Bitte zuerst „OSM-Daten laden“ ausführen.")
		}
		return netzBauen(pbf, hoehen, netz, logf)
	case "karte-werkzeuge":
		java, jar, err := sichereWerkzeuge(filepath.Join(arbeit, "werkzeuge"), logf)
		if err != nil {
			return err
		}
		e.mu.Lock()
		e.java, e.jar = java, jar
		e.mu.Unlock()
		return nil
	case "karte":
		if _, err := os.Stat(pbf); err != nil {
			return fmt.Errorf("OSM-Daten fehlen. Bitte zuerst „OSM-Daten laden“ ausführen.")
		}
		e.mu.Lock()
		java, jar := e.java, e.jar
		e.mu.Unlock()
		if java == "" || jar == "" {
			var err error
			java, jar, err = sichereWerkzeuge(filepath.Join(arbeit, "werkzeuge"), logf)
			if err != nil {
				return err
			}
		}
		return planetilerLauf(ctx, java, jar, pbf, filepath.Join(arbeit, "karte"), filepath.Join(arbeit, "werkzeuge", "quellen"), ein.PlanetilerXmxGB, ein.PlanetilerZoom, ein.PlanetilerMmap, logf)
	case "paket-karte":
		karteDatei := filepath.Join(arbeit, "karte", "germany.pmtiles")
		if _, err := os.Stat(karteDatei); err != nil {
			return fmt.Errorf("Die Karte fehlt. Bitte zuerst „Kacheln (PMTiles) bauen“ ausführen.")
		}
		version := time.Now().UTC().Format("2006-01-02T1504")
		grenze := int64(ein.MaxZipMB) * 1_000_000
		if grenze <= 0 {
			grenze = PaketGrenze
		}
		manifest, err := Packen("karte", version, filepath.Join(arbeit, "karte"), filepath.Join(pakete, "karte-"+version), []string{"germany.pmtiles"}, grenze, map[string]string{"quelle": "Planetiler " + planetilerVersion})
		if err != nil {
			return err
		}
		logf("Paket gepackt: %s", manifest)
		return nil
	case "schriften":
		return errAusstehend
	case "paket-netz":
		return paketNetzPacken(netz, pakete, ein.MaxZipMB, logf)
	case "hochladen":
		return hochladenNeuestesPaket(ein, pakete, logf)
	}
	return fmt.Errorf("unbekannter Schritt %s", id)
}

// paketNetzPacken: alle Dateien des Wegenetzes (ohne die Karte) in ein Paket
func paketNetzPacken(netz, pakete string, maxMB int, logf func(string, ...any)) error {
	var dateien []string
	err := filepath.Walk(netz, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(netz, p)
		dateien = append(dateien, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	if len(dateien) == 0 {
		return fmt.Errorf("im Wegenetz liegen keine Dateien")
	}
	sort.Strings(dateien)
	version := time.Now().UTC().Format("2006-01-02T1504")
	grenze := int64(maxMB) * 1_000_000
	if grenze <= 0 {
		grenze = PaketGrenze
	}
	ziel := filepath.Join(pakete, "netz-"+version)
	manifest, err := Packen("netz", version, netz, ziel, dateien, grenze, map[string]string{"quelle": "Wegenetz aus scripts/daten-bauen"})
	if err != nil {
		return err
	}
	logf("Paket gepackt: %s", manifest)
	return nil
}

// hochladenNeuestesPaket lädt das jüngste Wegenetz-Paket und das jüngste Karten-Paket auf den Server (sofern vorhanden)
func hochladenNeuestesPaket(ein Einstellungen, pakete string, logf func(string, ...any)) error {
	z := ein.Server
	zugang := ServerZugang{Host: z.Host, Port: z.Port, Benutzer: z.Benutzer, Schluessel: z.Schluessel, Ziel: z.ZielOrdner}
	if z.PasswortVerschl != "" {
		pw, err := Entschluesseln(z.PasswortVerschl)
		if err != nil {
			return err
		}
		zugang.Passwort = pw
	}
	if z.PassphraseVerschl != "" {
		pp, err := Entschluesseln(z.PassphraseVerschl)
		if err != nil {
			return err
		}
		zugang.Passphrase = pp
	}
	kh := z.KnownHosts
	if kh == "" {
		home, _ := os.UserHomeDir()
		kh = filepath.Join(home, ".ssh", "known_hosts")
	}
	var pakete2 []string
	for _, art := range []string{"netz-", "karte-"} {
		if neu := neuestesPaket(pakete, art); neu != "" {
			pakete2 = append(pakete2, neu)
		}
	}
	if len(pakete2) == 0 {
		return fmt.Errorf("kein Paket vorhanden. Bitte zuerst „Pakete packen“ ausführen.")
	}
	c, err := verbinden(zugang, kh, false)
	if err != nil {
		return err
	}
	defer c.Close()
	for _, name := range pakete2 {
		ordner := filepath.Join(pakete, name)
		version := strings.SplitN(name, "-", 2)[1]
		art := strings.SplitN(name, "-", 2)[0]
		manifestPfad := filepath.Join(ordner, art+"-"+version+".manifest.json")
		m, err := ladeManifest(manifestPfad)
		if err != nil {
			return err
		}
		// Platz prüfen: Teile, Entpacken und alte Version brauchen etwa das 2,2-fache der Paketgröße
		var gesamt int64
		for _, zi := range m.Zips {
			gesamt += zi.Bytes
		}
		frei, err := freierPlatz(c, z.ZielOrdner)
		if err == nil && frei < int64(float64(gesamt)*2.2) {
			return fmt.Errorf("auf dem Server fehlen etwa %d MB für %s", (int64(float64(gesamt)*2.2)-frei)/1_000_000, name)
		}
		logf("Lade %s hoch (%d ZIP-Dateien, %d MB)", name, len(m.Zips), gesamt/1_000_000)
		if err := PaketHochladen(c, z.ZielOrdner, name, ordner, m.Zips, manifestPfad, func(zip string, gesendet, groesse int64) {
			logf("%s: %d von %d MB", zip, gesendet/1_000_000, groesse/1_000_000)
		}); err != nil {
			return err
		}
	}
	return nil
}

// neuestesPaket liefert den jüngsten Paketordner mit dem Präfix (z. B. netz-), leer wenn keiner da ist
func neuestesPaket(pakete, praefix string) string {
	eintraege, err := os.ReadDir(pakete)
	if err != nil {
		return ""
	}
	var neueste string
	for _, e := range eintraege {
		if e.IsDir() && strings.HasPrefix(e.Name(), praefix) && e.Name() > neueste {
			neueste = e.Name()
		}
	}
	return neueste
}

// freierPlatz liefert den freien Platz (Bytes) am Zielordner auf dem Server
func freierPlatz(c *ssh.Client, ziel string) (int64, error) {
	out, err := befehl(c, "mkdir -p "+shellQuote(ziel)+" && df -B1 --output=avail "+shellQuote(ziel)+" | tail -1")
	if err != nil {
		return 0, err
	}
	var frei int64
	_, err = fmt.Sscan(strings.TrimSpace(out), &frei)
	return frei, err
}
