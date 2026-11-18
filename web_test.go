package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testWeb(t *testing.T) *webServer {
	t.Helper()
	ws := &webServer{token: "testtoken123", adresse: "127.0.0.1:5555", einst: StandardEinstellungen(t.TempDir()), einstPfad: filepath.Join(t.TempDir(), "e.json"), hub: neuerHub(), beendet: make(chan struct{})}
	ws.engine = neueEngine(ws.hub, ws.aktuelleEinstellungen)
	ws.ui = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")}}
	return ws
}

func anfrage(t *testing.T, h http.HandlerFunc, methode, pfad, body string, cookie bool, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(methode, pfad, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie {
		r.AddCookie(&http.Cookie{Name: "fdw_token", Value: "testtoken123"})
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func TestOhneTokenKeinZugriff(t *testing.T) {
	ws := testWeb(t)
	if rec := anfrage(t, ws.zugriff(ws.zustand), "GET", "/api/zustand", "", false, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("ohne Token: %d", rec.Code)
	}
	if rec := anfrage(t, ws.zugriff(ws.zustand), "GET", "/api/zustand", "", true, ""); rec.Code != http.StatusOK {
		t.Fatalf("mit Token: %d", rec.Code)
	}
}

func TestFremderAbsenderWirdAbgelehnt(t *testing.T) {
	ws := testWeb(t)
	h := ws.zugriff(ws.starten)
	if rec := anfrage(t, h, "POST", "/api/start", `{"schritte":[]}`, true, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("ohne Origin: %d", rec.Code)
	}
	if rec := anfrage(t, h, "POST", "/api/start", `{"schritte":[]}`, true, "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("fremder Origin: %d", rec.Code)
	}
	if rec := anfrage(t, h, "POST", "/api/start", `{"schritte":[]}`, true, "http://127.0.0.1:5555"); rec.Code == http.StatusForbidden {
		t.Fatalf("eigener Origin wurde abgelehnt")
	}
}

func TestPasswortWirdNieZurueckgegeben(t *testing.T) {
	ws := testWeb(t)
	h := ws.zugriff(ws.einstellungen)
	body := `{"arbeitsordner":"x","maxZipMB":200,"server":{"host":"h","port":22,"benutzer":"u","passwortMerken":false},"passwort":"GEHEIM-987"}`
	rec := anfrage(t, h, "PUT", "/api/einstellungen", body, true, "http://127.0.0.1:5555")
	if rec.Code != http.StatusOK {
		t.Fatalf("speichern: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "GEHEIM-987") {
		t.Fatal("Passwort taucht in der Antwort auf")
	}
	if strings.Contains(readFileOrEmpty(ws.einstPfad), "GEHEIM-987") {
		t.Fatal("Passwort steht im Klartext in der Einstellungsdatei")
	}
	zust := anfrage(t, ws.zugriff(ws.zustand), "GET", "/api/zustand", "", true, "")
	if strings.Contains(zust.Body.String(), "GEHEIM-987") {
		t.Fatal("Passwort taucht im Zustand auf")
	}
}

func TestStartlinieUndSeite(t *testing.T) {
	ws := testWeb(t)
	rec := anfrage(t, ws.index, "GET", "/?t=wrong", "", false, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("falsches Token auf der Seite: %d", rec.Code)
	}
	var _ fs.FS = ws.ui
}

func readFileOrEmpty(p string) string {
	b, err := readFileTest(p)
	if err != nil {
		return ""
	}
	return b
}

func readFileTest(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
