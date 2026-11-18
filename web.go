package main

// Lokale Oberfläche: HTTP-Server nur auf 127.0.0.1 mit zufälligem Port. Zugriff nur mit dem Sitzungs-Token (Cookie) und bei
// Änderungen nur mit passendem Absender (Origin). So kann keine fremde Webseite das Werkzeug fernsteuern.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type webServer struct {
	token     string
	adresse   string
	einst     Einstellungen
	einstPfad string
	einstMu   sync.Mutex
	hub       *Hub
	engine    *Engine
	server    *http.Server
	beendet   chan struct{}
	ui        fs.FS
}

func neuesToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StarteOberflaeche startet den Server, öffnet den Browser und blockiert, bis „Beenden“ gedrückt wird
func StarteOberflaeche(arbeitStandard string, ui fs.FS) error {
	pfad := EinstellungenDatei()
	einst, err := LadeEinstellungen(pfad, arbeitStandard)
	if err != nil {
		return fmt.Errorf("Einstellungen nicht lesbar: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	ws := &webServer{token: neuesToken(), adresse: ln.Addr().String(), einst: einst, einstPfad: pfad, hub: neuerHub(), beendet: make(chan struct{}), ui: ui}
	ws.engine = neueEngine(ws.hub, ws.aktuelleEinstellungen)
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.index)
	mux.HandleFunc("/api/zustand", ws.zugriff(ws.zustand))
	mux.HandleFunc("/api/einstellungen", ws.zugriff(ws.einstellungen))
	mux.HandleFunc("/api/server/testen", ws.zugriff(ws.serverTesten))
	mux.HandleFunc("/api/start", ws.zugriff(ws.starten))
	mux.HandleFunc("/api/abbrechen", ws.zugriff(ws.abbrechen))
	mux.HandleFunc("/api/ereignisse", ws.zugriff(ws.ereignisse))
	mux.HandleFunc("/api/beenden", ws.zugriff(ws.beenden))
	ws.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go ws.server.Serve(ln)

	url := "http://" + ws.adresse + "/?t=" + ws.token
	fmt.Println("Oberfläche: " + url)
	oeffneBrowser(url)
	<-ws.beendet
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ws.server.Shutdown(ctx)
}

// oeffneBrowser öffnet die Adresse im Standardbrowser (Windows); sonst nur ausgeben
func oeffneBrowser(url string) {
	if runtime.GOOS == "windows" {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}

func (w *webServer) aktuelleEinstellungen() Einstellungen {
	w.einstMu.Lock()
	defer w.einstMu.Unlock()
	return w.einst
}

// zugriff prüft Token und bei Änderungen den Absender
func (w *webServer) zugriff(h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("fdw_token")
		if err != nil || c.Value != w.token {
			http.Error(rw, "Zugriff verweigert", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			if r.Header.Get("Origin") != "http://"+w.adresse {
				http.Error(rw, "Zugriff verweigert (fremder Absender)", http.StatusForbidden)
				return
			}
		}
		h(rw, r)
	}
}

func (w *webServer) index(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	if t := r.URL.Query().Get("t"); t == w.token {
		http.SetCookie(rw, &http.Cookie{Name: "fdw_token", Value: w.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(rw, r, "/", http.StatusFound)
		return
	}
	c, err := r.Cookie("fdw_token")
	if err != nil || c.Value != w.token {
		http.Error(rw, "Zugriff verweigert. Bitte die Adresse aus dem Terminal oder der Startmeldung öffnen.", http.StatusForbidden)
		return
	}
	daten, err := fs.ReadFile(w.ui, "index.html")
	if err != nil {
		http.Error(rw, "Oberfläche fehlt", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
	rw.Write(daten)
}

func json2(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}

func (w *webServer) zustand(rw http.ResponseWriter, r *http.Request) {
	w.einstMu.Lock()
	sicht := OeffentlichSicht(w.einst)
	w.einstMu.Unlock()
	json2(rw, http.StatusOK, map[string]any{"einstellungen": sicht, "schritte": w.engine.StatusListe(), "laeuft": w.engine.Laeuft()})
}

// einstellungenAnfrage: Einstellungen plus die Geheimnisse, die nur beim Speichern vom Formular kommen
type einstellungenAnfrage struct {
	Einstellungen
	Passwort   string `json:"passwort"`
	Passphrase string `json:"passphrase"`
}

func (w *webServer) einstellungen(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(rw, "nur PUT", http.StatusMethodNotAllowed)
		return
	}
	var a einstellungenAnfrage
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 64<<10)).Decode(&a); err != nil {
		json2(rw, http.StatusBadRequest, map[string]string{"fehler": "Eingabe ungültig"})
		return
	}
	w.einstMu.Lock()
	defer w.einstMu.Unlock()
	neu := a.Einstellungen
	// Geheimnisse: vorhandene behalten, wenn nichts Neues eingegeben wurde; nur mit „merken“ speichern
	neu.Server.PasswortVerschl = w.einst.Server.PasswortVerschl
	neu.Server.PassphraseVerschl = w.einst.Server.PassphraseVerschl
	if !neu.Server.PasswortMerken {
		neu.Server.PasswortVerschl = ""
	}
	if a.Passwort != "" && neu.Server.PasswortMerken {
		v, err := Verschluesseln(a.Passwort)
		if err != nil {
			json2(rw, http.StatusBadRequest, map[string]string{"fehler": err.Error()})
			return
		}
		neu.Server.PasswortVerschl = v
	}
	if a.Passphrase != "" && neu.Server.PasswortMerken {
		v, err := Verschluesseln(a.Passphrase)
		if err != nil {
			json2(rw, http.StatusBadRequest, map[string]string{"fehler": err.Error()})
			return
		}
		neu.Server.PassphraseVerschl = v
	}
	if !neu.Server.PasswortMerken {
		neu.Server.PassphraseVerschl = ""
	}
	if err := SpeichereEinstellungen(w.einstPfad, neu); err != nil {
		json2(rw, http.StatusInternalServerError, map[string]string{"fehler": "Speichern fehlgeschlagen"})
		return
	}
	w.einst = neu
	json2(rw, http.StatusOK, OeffentlichSicht(neu))
}

// serverTesten: Anmeldung, Host-Schlüssel und freier Platz am Zielordner prüfen
func (w *webServer) serverTesten(rw http.ResponseWriter, r *http.Request) {
	var a struct {
		Vertrauen  bool   `json:"vertrauen"`
		Passwort   string `json:"passwort"`
		Passphrase string `json:"passphrase"`
	}
	json.NewDecoder(http.MaxBytesReader(rw, r.Body, 16<<10)).Decode(&a)
	zugang, kh, err := w.zugang(a.Passwort, a.Passphrase)
	if err != nil {
		json2(rw, http.StatusOK, map[string]any{"ok": false, "fehler": err.Error()})
		return
	}
	c, err := verbinden(zugang, kh, a.Vertrauen)
	if err != nil {
		if u, ok := err.(*UnbekannterHostError); ok {
			json2(rw, http.StatusOK, map[string]any{"ok": false, "unbekannt": true, "fingerabdruck": u.Fingerabdruck, "fehler": u.Error()})
			return
		}
		json2(rw, http.StatusOK, map[string]any{"ok": false, "fehler": err.Error()})
		return
	}
	defer c.Close()
	out, err := befehl(c, "mkdir -p "+shellQuote(zugang.Ziel)+" && df -B1 --output=avail "+shellQuote(zugang.Ziel)+" | tail -1")
	if err != nil {
		json2(rw, http.StatusOK, map[string]any{"ok": false, "fehler": "Zielordner nicht nutzbar: " + out})
		return
	}
	var frei int64
	fmt.Sscan(strings.TrimSpace(out), &frei)
	json2(rw, http.StatusOK, map[string]any{"ok": true, "frei": frei})
}

// zugang baut die Verbindungsangaben aus den Einstellungen; Passwörter aus dem Formular haben Vorrang
func (w *webServer) zugang(passwort, passphrase string) (ServerZugang, string, error) {
	e := w.aktuelleEinstellungen()
	s := e.Server
	z := ServerZugang{Host: s.Host, Port: s.Port, Benutzer: s.Benutzer, Schluessel: s.Schluessel, Ziel: s.ZielOrdner}
	if passwort != "" {
		z.Passwort = passwort
	} else if s.PasswortVerschl != "" {
		pw, err := Entschluesseln(s.PasswortVerschl)
		if err != nil {
			return z, "", err
		}
		z.Passwort = pw
	}
	if passphrase != "" {
		z.Passphrase = passphrase
	} else if s.PassphraseVerschl != "" {
		pp, err := Entschluesseln(s.PassphraseVerschl)
		if err != nil {
			return z, "", err
		}
		z.Passphrase = pp
	}
	kh := s.KnownHosts
	if kh == "" {
		home, _ := os.UserHomeDir()
		kh = filepath.Join(home, ".ssh", "known_hosts")
	}
	return z, kh, nil
}

func (w *webServer) starten(rw http.ResponseWriter, r *http.Request) {
	var a struct {
		Schritte []string `json:"schritte"`
	}
	json.NewDecoder(http.MaxBytesReader(rw, r.Body, 16<<10)).Decode(&a)
	if err := w.engine.Start(a.Schritte); err != nil {
		json2(rw, http.StatusConflict, map[string]string{"fehler": err.Error()})
		return
	}
	json2(rw, http.StatusAccepted, map[string]bool{"gestartet": true})
}

func (w *webServer) abbrechen(rw http.ResponseWriter, r *http.Request) {
	w.engine.Abbrechen()
	json2(rw, http.StatusOK, map[string]bool{"ok": true})
}

func (w *webServer) beenden(rw http.ResponseWriter, r *http.Request) {
	json2(rw, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(w.beendet)
	}()
}

// ereignisse: Server-Sent Events mit allen Statusmeldungen
func (w *webServer) ereignisse(rw http.ResponseWriter, r *http.Request) {
	fl, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "Streaming nicht möglich", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-store")
	kanal := w.hub.Abonnieren()
	defer w.hub.Beenden(kanal)
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-kanal:
			daten, _ := json.Marshal(e)
			fmt.Fprintf(rw, "data: %s\n\n", daten)
			fl.Flush()
		}
	}
}
