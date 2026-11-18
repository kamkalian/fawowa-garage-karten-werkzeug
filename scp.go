package main

// Hochladen der Pakete auf den Server per SCP (eigenes Protokoll über eine SSH-Sitzung) mit Prüfsummen und Wiederaufnahme.
// Ablauf je ZIP: auf dem Server als „<name>.tmp“ übertragen, SHA-256 vergleichen, dann umbenennen. Gibt es die Datei schon mit
// gleicher Größe und Prüfsumme, wird sie übersprungen. So lässt sich ein abgebrochener Upload fortsetzen.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ServerZugang beschreibt die Verbindung zum Server
type ServerZugang struct {
	Host       string
	Port       int
	Benutzer   string
	Schluessel string // Pfad zur privaten Schlüsseldatei (optional)
	Passphrase string
	Ziel       string // Zielordner auf dem Server, z. B. /srv/apps/garage/data/import
}

// UnbekannterHostError: der Server ist noch nicht in known_hosts; der Fingerabdruck muss bestätigt werden
type UnbekannterHostError struct {
	Host          string
	Fingerabdruck string
}

func (e *UnbekannterHostError) Error() string {
	return fmt.Sprintf("Der Server %s ist unbekannt (Fingerabdruck %s). Bitte bestätigen.", e.Host, e.Fingerabdruck)
}

// hostKeyPruefung prüft den Host-Schlüssel gegen known_hosts. Unbekannt → UnbekannterHostError (oder, mit vertrauen, speichern).
func hostKeyPruefung(knownHostsDatei string, vertrauen bool, merken func(*UnbekannterHostError)) ssh.HostKeyCallback {
	return func(host string, remote net.Addr, key ssh.PublicKey) error {
		if _, err := os.Stat(knownHostsDatei); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(knownHostsDatei, nil, 0o600); err != nil {
				return err
			}
		}
		cb, err := knownhosts.New(knownHostsDatei)
		if err != nil {
			return err
		}
		err = cb(host, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) || len(ke.Want) > 0 {
			// Schlüssel geändert: immer abbrechen
			return fmt.Errorf("Der Host-Schlüssel von %s hat sich geändert. Verbindung abgebrochen (mögliche Gefahr).", host)
		}
		fp := ssh.FingerprintSHA256(key)
		if !vertrauen {
			u := &UnbekannterHostError{Host: host, Fingerabdruck: fp}
			merken(u)
			return u
		}
		f, err := os.OpenFile(knownHostsDatei, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(host)}, key))
		return err
	}
}

// verbinden öffnet die SSH-Verbindung mit dem Schlüssel
func verbinden(z ServerZugang, knownHostsDatei string, vertrauen bool) (*ssh.Client, error) {
	var unbekannt *UnbekannterHostError
	var methoden []ssh.AuthMethod
	if z.Schluessel != "" {
		daten, err := os.ReadFile(z.Schluessel)
		if err != nil {
			return nil, fmt.Errorf("Schlüsseldatei nicht lesbar: %w", err)
		}
		var signer ssh.Signer
		if z.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(daten, []byte(z.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(daten)
		}
		if err != nil {
			return nil, fmt.Errorf("Schlüssel nicht lesbar: %w", err)
		}
		methoden = append(methoden, ssh.PublicKeys(signer))
	}
	if len(methoden) == 0 {
		return nil, errors.New("Kein Schlüssel angegeben")
	}
	port := z.Port
	if port == 0 {
		port = 22
	}
	cfg := &ssh.ClientConfig{
		User:            z.Benutzer,
		Auth:            methoden,
		HostKeyCallback: hostKeyPruefung(knownHostsDatei, vertrauen, func(u *UnbekannterHostError) { unbekannt = u }),
		Timeout:         20 * time.Second,
	}
	c, err := ssh.Dial("tcp", net.JoinHostPort(z.Host, strconv.Itoa(port)), cfg)
	if err != nil && unbekannt != nil {
		return nil, unbekannt
	}
	return c, err
}

// shellQuote setzt einen Pfad für die Shell des Servers in einfache Anführungszeichen
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// befehl führt einen Befehl auf dem Server aus und liefert die Ausgabe
func befehl(c *ssh.Client, cmd string) (string, error) {
	s, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	out, err := s.CombinedOutput(cmd)
	return strings.TrimSpace(string(out)), err
}

// remoteSha liefert SHA-256 und Größe einer Datei auf dem Server; ok = false, wenn sie fehlt
func remoteSha(c *ssh.Client, pfad string) (sum string, groesse int64, ok bool, err error) {
	out, err := befehl(c, "test -f "+shellQuote(pfad)+" && stat -c %s "+shellQuote(pfad)+" && sha256sum "+shellQuote(pfad)+" | cut -d' ' -f1")
	if err != nil {
		return "", 0, false, nil // Datei fehlt (test schlägt fehl)
	}
	zeilen := strings.Split(out, "\n")
	if len(zeilen) < 2 {
		return "", 0, false, fmt.Errorf("unerwartete Antwort: %q", out)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(zeilen[0]), 10, 64)
	if err != nil {
		return "", 0, false, err
	}
	return strings.TrimSpace(zeilen[1]), n, true, nil
}

// scpSchreiben überträgt eine Datei mit dem SCP-Protokoll (Sink-Modus auf dem Server: „scp -t <ordner>“)
func scpSchreiben(c *ssh.Client, ordner, name, quelle string, fortschritt func(gesendet, gesamt int64)) error {
	f, err := os.Open(quelle)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	s, err := c.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	in, err := s.StdinPipe()
	if err != nil {
		return err
	}
	out, err := s.StdoutPipe()
	if err != nil {
		return err
	}
	if err := s.Start("scp -t " + shellQuote(ordner)); err != nil {
		return err
	}
	r := bufio.NewReader(out)
	if err := scpAntwort(r); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(in, "C0644 %d %s\n", info.Size(), name); err != nil {
		return err
	}
	if err := scpAntwort(r); err != nil {
		return err
	}
	var gesendet int64
	schreiber := &zaehlerSchreiber{w: in, f: func(n int64) {
		gesendet += n
		fortschritt(gesendet, info.Size())
	}}
	if _, err := io.Copy(schreiber, f); err != nil {
		return err
	}
	if _, err := in.Write([]byte{0}); err != nil {
		return err
	}
	if err := scpAntwort(r); err != nil {
		return err
	}
	in.Close()
	return s.Wait()
}

type zaehlerSchreiber struct {
	w io.Writer
	f func(n int64)
}

func (z *zaehlerSchreiber) Write(p []byte) (int, error) {
	n, err := z.w.Write(p)
	if n > 0 {
		z.f(int64(n))
	}
	return n, err
}

// scpAntwort liest die Bestätigung des Servers: 0 = ok, 1 oder 2 = Fehler mit Text
func scpAntwort(r *bufio.Reader) error {
	b, err := r.ReadByte()
	if err != nil {
		return err
	}
	if b == 0 {
		return nil
	}
	text, _ := r.ReadString('\n')
	return fmt.Errorf("Server meldet Fehler: %s", strings.TrimSpace(text))
}

// PaketHochladen überträgt ein Paket (Manifest und ZIPs) in den Ordner <ziel>/<paketname>.hochladen und benennt den Ordner
// erst am Ende um. So sieht der Server das Paket erst, wenn alles vollständig und geprüft ist.
func PaketHochladen(c *ssh.Client, zielOrdner, paketname, lokalerOrdner string, zips []ZipEintrag, manifest string, fortschritt func(zip string, gesendet, gesamt int64)) error {
	hochladen := path.Join(zielOrdner, paketname+".hochladen")
	fertig := path.Join(zielOrdner, paketname)
	if _, err := befehl(c, "mkdir -p "+shellQuote(hochladen)); err != nil {
		return fmt.Errorf("Ordner auf dem Server nicht anlegbar: %w", err)
	}
	for _, z := range zips {
		lokal := filepath.Join(lokalerOrdner, z.Name)
		ziel := path.Join(hochladen, z.Name)
		sum, groesse, ok, _ := remoteSha(c, ziel)
		if ok && groesse == z.Bytes && sum == z.Sha256 {
			continue // schon vollständig übertragen (Wiederaufnahme)
		}
		var letzterFehler error
		for versuch := 1; versuch <= 3; versuch++ {
			err := scpSchreiben(c, hochladen, z.Name+".tmp", lokal, func(g, t int64) { fortschritt(z.Name, g, t) })
			if err != nil {
				letzterFehler = err
				continue
			}
			tmpSum, tmpGroesse, tmpOk, _ := remoteSha(c, ziel+".tmp")
			if tmpOk && tmpGroesse == z.Bytes && tmpSum == z.Sha256 {
				if _, err := befehl(c, "mv -f "+shellQuote(ziel+".tmp")+" "+shellQuote(ziel)); err != nil {
					return err
				}
				letzterFehler = nil
				break
			}
			letzterFehler = fmt.Errorf("Prüfsumme von %s stimmt nicht", z.Name)
		}
		if letzterFehler != nil {
			return fmt.Errorf("%s: %w", z.Name, letzterFehler)
		}
	}
	// Manifest zuletzt; danach wird der Ordner atomar umbenannt
	if err := scpDateiEinfach(c, hochladen, filepath.Base(manifest), manifest); err != nil {
		return err
	}
	if _, err := befehl(c, "rm -rf "+shellQuote(fertig)+" && mv "+shellQuote(hochladen)+" "+shellQuote(fertig)); err != nil {
		return err
	}
	return nil
}

func scpDateiEinfach(c *ssh.Client, ordner, name, lokal string) error {
	return scpSchreiben(c, ordner, name, lokal, func(int64, int64) {})
}

// sha256Remote berechnet die Prüfsumme einer lokalen Datei (für den Vergleich nach dem Upload)
func sha256Lokal(pfad string) (string, error) {
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
