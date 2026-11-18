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
	"sync/atomic"
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
	k := make(chan Ereignis, 1024)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kanaele[k] = true
	// Die letzten Ereignisse gehen an die neue Verbindung, damit die Seite nach dem Neuladen zeigt, was zuletzt passiert ist
	for _, e := range h.letzte {
		select {
		case k <- e:
		default:
		}
	}
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
	neu     map[string]bool // Schritte, deren Rohdaten neu geladen werden
}

func neueEngine(hub *Hub, einst func() Einstellungen) *Engine {
	e := &Engine{hub: hub, status: map[string]string{}, einst: einst}
	for _, s := range schrittListe {
		e.status[s.ID] = "offen"
	}
	return e
}

// StatusListe liefert Schritte mit Status, Stand der Daten und letztem Lauf (für die Oberfläche)
func (e *Engine) StatusListe() []map[string]any {
	e.mu.Lock()
	status := make(map[string]string, len(e.status))
	for k, v := range e.status {
		status[k] = v
	}
	e.mu.Unlock()
	arbeit, _ := filepath.Abs(e.einst().Arbeitsordner)
	var out []map[string]any
	for _, s := range schrittListe {
		eintrag := map[string]any{"id": s.ID, "name": s.Name, "status": status[s.ID], "daten": datenStand(s.ID, arbeit)}
		if l := schrittStandLesen(arbeit, s.ID).Letzter; l != nil {
			eintrag["letzter"] = l
		}
		out = append(out, eintrag)
	}
	return out
}

func (e *Engine) Laeuft() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.laeuft
}

// Start startet die gewählten Schritte (in der Reihenfolge der Liste). Läuft schon etwas, passiert nichts.
func (e *Engine) Start(gewaehlt, neu []string) error {
	e.mu.Lock()
	if e.laeuft {
		e.mu.Unlock()
		return fmt.Errorf("Es läuft schon ein Ablauf.")
	}
	ctx, abbruch := context.WithCancel(context.Background())
	e.laeuft = true
	e.abbruch = abbruch
	e.neu = map[string]bool{}
	for _, id := range neu {
		e.neu[id] = true
	}
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
		arbeit, _ := filepath.Abs(e.einst().Arbeitsordner)
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
			start := time.Now()
			err := e.fuehreAus(ctx, s.ID)
			switch {
			case err == errAusstehend:
				e.setze(s.ID, "ausstehend")
				letzterLaufNotieren(arbeit, s.ID, start, "ausstehend", "")
			case ctx.Err() != nil:
				e.setze(s.ID, "offen")
				letzterLaufNotieren(arbeit, s.ID, start, "abgebrochen", "")
				e.protokoll("Abgebrochen.")
				return
			case err != nil:
				e.setze(s.ID, "fehler")
				letzterLaufNotieren(arbeit, s.ID, start, "fehler", err.Error())
				e.protokoll("Fehler in „" + s.Name + "“: " + err.Error())
				return
			default:
				e.setze(s.ID, "fertig")
				letzterLaufNotieren(arbeit, s.ID, start, "fertig", "")
			}
		}
	}()
	return nil
}

// wirdNeu: die Rohdaten dieses Schritts sollen neu geladen werden (sonst nur, wenn es neuere gibt)
func (e *Engine) wirdNeu(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.neu[id]
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
		_, _, err := ladeDatei(kartenUrl, pbf, false, e.wirdNeu(id), "OSM", logf)
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
			if _, _, err := ladeDatei(url, filepath.Join(hoehen, n+".hgt.gz"), true, e.wirdNeu(id), "", logf); err != nil {
				return err
			}
			e.hub.Senden(Ereignis{Art: "fortschritt", Schritt: id, Anteil: float64(i+1) / float64(len(namen)), Text: fmt.Sprintf("%d von %d Kacheln", i+1, len(namen))})
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
		return hochladenNeuestesPaket(ctx, ein, pakete, arbeit, logf, func(text string, anteil float64) {
			e.hub.Senden(Ereignis{Art: "fortschritt", Schritt: id, Anteil: anteil, Text: text})
		})
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

// hochladenNeuestesPaket lädt das jüngste Wegenetz-Paket und das jüngste Karten-Paket auf den Server (sofern vorhanden).
// Der Fortschritt kommt höchstens zweimal pro Sekunde. Bleibt er fünf Minuten aus, wird die Verbindung geschlossen, damit der
// Ablauf nicht hängen bleibt. Abbrechen beendet die Übertragung ebenfalls.
func hochladenNeuestesPaket(ctx context.Context, ein Einstellungen, pakete, arbeit string, logf func(string, ...any), fortschritt func(text string, anteil float64)) error {
	z := ein.Server
	zugang := ServerZugang{Host: z.Host, Port: z.Port, Benutzer: z.Benutzer, Schluessel: z.Schluessel, Ziel: z.ZielOrdner}
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
	logf("Verbinde mit dem Server ...")
	c, err := verbinden(zugang, kh, false)
	if err != nil {
		return err
	}
	defer c.Close()
	logf("Verbunden.")

	// Letzte Aktivität (Unix-Nanosekunden): jede Fortschrittsmeldung und jeder Paketstart zählt
	var aktiv atomic.Int64
	aktiv.Store(time.Now().UnixNano())
	stopp := make(chan struct{})
	defer close(stopp)
	go func() {
		pruefen := time.NewTicker(10 * time.Second)
		halten := time.NewTicker(20 * time.Second)
		defer pruefen.Stop()
		defer halten.Stop()
		for {
			select {
			case <-stopp:
				return
			case <-ctx.Done():
				c.Close()
				return
			case <-pruefen.C:
				if time.Since(time.Unix(0, aktiv.Load())) > 5*time.Minute {
					logf("Seit 5 Minuten keine Übertragung. Die Verbindung wird geschlossen.")
					c.Close()
					return
				}
			case <-halten.C:
				// Keepalive: antwortet der Server nicht mehr, ist die Verbindung tot. Eigene Routine, damit die Prüfung weiterläuft.
				go func() {
					if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
						c.Close()
					}
				}()
			}
		}
	}()

	for _, name := range pakete2 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
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
		logf("Lade %s hoch (%d ZIP-Dateien, %s)", name, len(m.Zips), groesseText(gesamt))
		aktiv.Store(time.Now().UnixNano())

		// Fortschritt gedrosselt: höchstens alle halbe Sekunde eine Meldung; die Geschwindigkeit gilt seit der letzten Meldung
		var letzterZip string
		var letzteZeit time.Time
		var letzterStand int64
		zipFortschritt := func(zip string, gesendet, groesse int64) {
			jetzt := time.Now()
			aktiv.Store(jetzt.UnixNano())
			if zip != letzterZip {
				letzterZip = zip
				letzteZeit = jetzt.Add(-time.Second)
				letzterStand = 0
			}
			if gesendet < groesse && jetzt.Sub(letzteZeit) < 500*time.Millisecond {
				return
			}
			sek := jetzt.Sub(letzteZeit).Seconds()
			if sek < 0.001 {
				sek = 0.001
			}
			rate := int64(float64(gesendet-letzterStand) / sek)
			letzteZeit, letzterStand = jetzt, gesendet
			anteil := 0.0
			if groesse > 0 {
				anteil = float64(gesendet) / float64(groesse)
			}
			fortschritt(fmt.Sprintf("%s: %s von %s · %s/s", zip, groesseText(gesendet), groesseText(groesse), groesseText(rate)), anteil)
		}
		if err := PaketHochladen(c, z.ZielOrdner, name, ordner, m.Zips, manifestPfad, zipFortschritt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		paketHochgeladenNotieren(arbeit, name)
		logf("%s ist auf dem Server.", name)
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
