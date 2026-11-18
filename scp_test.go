package main

// Tests für das Hochladen per SCP gegen einen kleinen SSH-Server im Test. Der Server führt nur die wenigen Befehle aus, die das
// Werkzeug braucht (mkdir, Prüfen, mv, rm, scp -t), und schreibt in ein temporäres Verzeichnis.

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testServer: SSH-Server, der Befehle selbst ausführt
type testServer struct {
	t           *testing.T
	ln          net.Listener
	adresse     string
	host        ssh.PublicKey
	verfaelscht int // so viele Dateiübertragungen werden absichtlich verfälscht
	schluessel  string
}

func neuerTestServer(t *testing.T, verfaelscht int) *testServer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cpub, cpriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cpubSSH, err := ssh.NewPublicKey(cpub)
	if err != nil {
		t.Fatal(err)
	}
	schluessel := testSchluesselDatei(t, cpriv)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "test" && string(key.Marshal()) == string(cpubSSH.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("falsch")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testServer{t: t, ln: ln, adresse: ln.Addr().String(), host: signer.PublicKey(), verfaelscht: verfaelscht, schluessel: schluessel}
	go func() {
		for {
			tcp, err := ln.Accept()
			if err != nil {
				return
			}
			go s.verbindung(tcp, cfg)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *testServer) verbindung(tcp net.Conn, cfg *ssh.ServerConfig) {
	sc, kanaele, anfragen, err := ssh.NewServerConn(tcp, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(anfragen)
	for neu := range kanaele {
		if neu.ChannelType() != "session" {
			neu.Reject(ssh.UnknownChannelType, "nur Sitzungen")
			continue
		}
		kanal, reqs, err := neu.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range reqs {
				if r.Type != "exec" {
					r.Reply(false, nil)
					continue
				}
				r.Reply(true, nil)
				var p struct{ Befehl string }
				ssh.Unmarshal(r.Payload, &p)
				code := s.ausfuehren(kanal, p.Befehl)
				kanal.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(code)}))
				kanal.Close()
			}
		}()
	}
}

var (
	reScpZiel = regexp.MustCompile(`^scp -t '(.*)'$`)
	reMkdir   = regexp.MustCompile(`^mkdir -p '(.*)'$`)
	rePruefen = regexp.MustCompile(`^test -f '(.+?)' && stat -c %s '(.+?)' && sha256sum '(.+?)' \| cut -d' ' -f1$`)
	reMv      = regexp.MustCompile(`^mv -f '(.*)' '(.*)'$`)
	reRmMv    = regexp.MustCompile(`^rm -rf '(.*)' && mv '(.*)' '(.*)'$`)
)

// ausfuehren: die wenigen Befehle, die das Werkzeug auf dem Server braucht; liefert den Exit-Code
func (s *testServer) ausfuehren(kanal ssh.Channel, befehl string) int {
	if m := reScpZiel.FindStringSubmatch(befehl); m != nil {
		return s.scpEmpfangen(kanal, m[1])
	}
	if m := reMkdir.FindStringSubmatch(befehl); m != nil {
		if os.MkdirAll(m[1], 0o755) != nil {
			return 1
		}
		return 0
	}
	if m := rePruefen.FindStringSubmatch(befehl); m != nil {
		daten, err := os.ReadFile(m[1])
		if err != nil {
			return 1
		}
		sum := sha256.Sum256(daten)
		fmt.Fprintf(kanal, "%d\n%x\n", len(daten), sum)
		return 0
	}
	if m := reMv.FindStringSubmatch(befehl); m != nil {
		if os.Rename(m[1], m[2]) != nil {
			return 1
		}
		return 0
	}
	if m := reRmMv.FindStringSubmatch(befehl); m != nil {
		os.RemoveAll(m[1])
		if os.Rename(m[2], m[3]) != nil {
			return 1
		}
		return 0
	}
	fmt.Fprintf(kanal.Stderr(), "unbekannter Befehl im Test: %s\n", befehl)
	return 127
}

// scpEmpfangen: die Gegenstelle von „scp -t“
func (s *testServer) scpEmpfangen(kanal ssh.Channel, ordner string) int {
	r := bufio.NewReader(kanal)
	kanal.Write([]byte{0})
	kopf, err := r.ReadString('\n')
	if err != nil {
		return 1
	}
	teile := strings.Fields(kopf)
	if len(teile) != 3 {
		return 1
	}
	groesse, _ := strconv.ParseInt(teile[1], 10, 64)
	name := teile[2]
	kanal.Write([]byte{0})
	daten := make([]byte, groesse)
	if _, err := io.ReadFull(r, daten); err != nil {
		return 1
	}
	if _, err := r.ReadByte(); err != nil {
		return 1
	}
	if s.verfaelscht > 0 && len(daten) > 0 {
		s.verfaelscht--
		daten[0] ^= 0xFF // absichtlich falsch übertragen
	}
	if err := os.WriteFile(filepath.Join(ordner, name), daten, 0o644); err != nil {
		kanal.Write([]byte("\x01schreiben fehlgeschlagen\n"))
		return 1
	}
	kanal.Write([]byte{0})
	return 0
}

// testZip erzeugt eine Datei mit n Bytes als „ZIP“ im Zielordner; liefert den Eintrag für das Manifest
func testZip(t *testing.T, ordner, name string, n int) ZipEintrag {
	t.Helper()
	daten := make([]byte, n)
	for i := range daten {
		daten[i] = byte(i*7 + n)
	}
	pfad := filepath.Join(ordner, name)
	if err := os.WriteFile(pfad, daten, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256Lokal(pfad)
	if err != nil {
		t.Fatal(err)
	}
	return ZipEintrag{Name: name, Bytes: int64(n), Sha256: sum}
}

func zugangFuer(s *testServer, ziel string) (ServerZugang, string) {
	host, port, _ := net.SplitHostPort(s.adresse)
	p, _ := strconv.Atoi(port)
	return ServerZugang{Host: host, Port: p, Benutzer: "test", Schluessel: s.schluessel, Ziel: ziel}, filepath.Join(os.TempDir(), "kh-"+port)
}

func TestHostSchluesselUnbekanntWirdAbgefragt(t *testing.T) {
	s := neuerTestServer(t, 0)
	z, kh := zugangFuer(s, t.TempDir())
	t.Cleanup(func() { os.Remove(kh) })
	_, err := verbinden(z, kh, false)
	if _, ok := err.(*UnbekannterHostError); !ok {
		t.Fatalf("erwartet UnbekannterHostError, bekommen: %v", err)
	}
	// Nach Bestätigung klappt die Verbindung
	c, err := verbinden(z, kh, true)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// Danach auch ohne erneute Bestätigung
	c2, err := verbinden(z, kh, false)
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()
}

func TestPaketHochladenMitWiederaufnahme(t *testing.T) {
	s := neuerTestServer(t, 0)
	z, kh := zugangFuer(s, t.TempDir())
	t.Cleanup(func() { os.Remove(kh) })
	c, err := verbinden(z, kh, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	lokal := t.TempDir()
	zips := []ZipEintrag{testZip(t, lokal, "netz-v1.teil-001-von-002.zip", 300_000), testZip(t, lokal, "netz-v1.teil-002-von-002.zip", 120_000)}
	manifest := filepath.Join(lokal, "netz-v1.manifest.json")
	os.WriteFile(manifest, []byte(`{"format":"fawowa-paket-1"}`), 0o644)

	ziel := t.TempDir()
	// Erster Lauf: die erste ZIP ist schon vollständig auf dem Server (Wiederaufnahme)
	hochladen := filepath.Join(ziel, "netz-v1.hochladen")
	os.MkdirAll(hochladen, 0o755)
	os.WriteFile(filepath.Join(hochladen, zips[0].Name), mustRead(t, filepath.Join(lokal, zips[0].Name)), 0o644)

	if err := PaketHochladen(c, ziel, "netz-v1", lokal, zips, manifest, func(string, int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	fertig := filepath.Join(ziel, "netz-v1")
	for _, z := range zips {
		if !bytesGleich(mustRead(t, filepath.Join(fertig, z.Name)), mustRead(t, filepath.Join(lokal, z.Name))) {
			t.Fatalf("%s stimmt auf dem Server nicht", z.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(fertig, "netz-v1.manifest.json")); err != nil {
		t.Fatal("Manifest fehlt auf dem Server")
	}
	if _, err := os.Stat(hochladen); !os.IsNotExist(err) {
		t.Fatal("der Hochlade-Ordner wurde nicht umbenannt")
	}
}

func TestPaketHochladenWiederholtBeiFalscherPruefsumme(t *testing.T) {
	s := neuerTestServer(t, 1) // die erste Übertragung kommt verfälscht an
	z, kh := zugangFuer(s, t.TempDir())
	t.Cleanup(func() { os.Remove(kh) })
	c, err := verbinden(z, kh, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lokal := t.TempDir()
	zips := []ZipEintrag{testZip(t, lokal, "karte-v1.teil-001-von-001.zip", 50_000)}
	manifest := filepath.Join(lokal, "karte-v1.manifest.json")
	os.WriteFile(manifest, []byte("{}"), 0o644)
	ziel := t.TempDir()
	if err := PaketHochladen(c, ziel, "karte-v1", lokal, zips, manifest, func(string, int64, int64) {}); err != nil {
		t.Fatalf("erneuter Versuch hat nicht geholfen: %v", err)
	}
	if !bytesGleich(mustRead(t, filepath.Join(ziel, "karte-v1", zips[0].Name)), mustRead(t, filepath.Join(lokal, zips[0].Name))) {
		t.Fatal("Datei nach dem erneuten Versuch falsch")
	}
}

func TestPaketHochladenBleibtBeiDauerhaftFalscherPruefsumme(t *testing.T) {
	s := neuerTestServer(t, 10) // alle Versuche kommen verfälscht an
	z, kh := zugangFuer(s, t.TempDir())
	t.Cleanup(func() { os.Remove(kh) })
	c, err := verbinden(z, kh, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lokal := t.TempDir()
	zips := []ZipEintrag{testZip(t, lokal, "karte-v2.teil-001-von-001.zip", 20_000)}
	manifest := filepath.Join(lokal, "karte-v2.manifest.json")
	os.WriteFile(manifest, []byte("{}"), 0o644)
	ziel := t.TempDir()
	if err := PaketHochladen(c, ziel, "karte-v2", lokal, zips, manifest, func(string, int64, int64) {}); err == nil {
		t.Fatal("dauerhaft falsche Prüfsumme wurde nicht gemeldet")
	}
	if _, err := os.Stat(filepath.Join(ziel, "karte-v2")); !os.IsNotExist(err) {
		t.Fatal("unvollständiges Paket wurde sichtbar gemacht")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bytesGleich(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
