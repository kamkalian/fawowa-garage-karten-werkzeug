package main

// Grundkarte aus OpenStreetMap: Flächen (Wasser, Wald, Grün, Wohngebiete) und Linien (Flüsse, Bäche, Bahn, Autobahn).
// Entspricht tools/routing/karte-bauen.js. Geschrieben wird nach <Ausgabe>/karte.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"
)

var karteSchluessel = map[string]bool{
	"natural": true, "landuse": true, "leisure": true, "waterway": true, "railway": true, "water": true, "highway": true, "type": true, "motorroad": true,
}

var gruenLanduse = setzeAus("grass", "meadow", "recreation_ground", "village_green", "cemetery", "orchard", "vineyard", "allotments")
var gruenLeisure = setzeAus("park", "garden", "playground", "recreation_ground", "pitch")

// merkmal ist die Klasse eines Weges: 1 Wasser, 2 Wald, 3 Grün, 4 Wohngebiet (Flächen); 5 Fluss, 6 Bach, 7 Bahn, 8 Autobahn (Linien)
type merkmal struct {
	flaeche bool
	cls     int
}

func klassifiziere(t map[string]string) *merkmal {
	nat := t["natural"]
	land := t["landuse"]
	if nat == "water" || t["water"] != "" || land == "reservoir" || t["waterway"] == "riverbank" {
		return &merkmal{true, 1}
	}
	if land == "forest" || nat == "wood" {
		return &merkmal{true, 2}
	}
	if gruenLanduse[land] || gruenLeisure[t["leisure"]] {
		return &merkmal{true, 3}
	}
	if land == "residential" {
		return &merkmal{true, 4}
	}
	ww := t["waterway"]
	if ww == "river" || ww == "canal" {
		return &merkmal{false, 5}
	}
	if ww == "stream" {
		return &merkmal{false, 6}
	}
	if t["railway"] == "rail" {
		return &merkmal{false, 7}
	}
	hw := t["highway"]
	if hw == "motorway" || hw == "motorway_link" || hw == "trunk" || hw == "trunk_link" || t["motorroad"] == "yes" {
		return &merkmal{false, 8}
	}
	return nil
}

// karteBauer sammelt die Merkmale der Karte
type karteBauer struct {
	pts       []int32
	off       []uint32
	meta      []uint8
	bbox      []float32
	cosBreite float64
}

// vereinfache: Douglas-Peucker; Abstand in Grad, Längen mit cos(Breite) gestaucht. Ergebnis: Maske der behaltenen Punkte
func vereinfache(a, o []float64, eps float64) []bool {
	n := len(a)
	keep := make([]bool, n)
	keep[0] = true
	keep[n-1] = true
	c := jsCos(51.3 * piJS / 180)
	stapel := [][2]int{{0, n - 1}}
	for len(stapel) > 0 {
		top := stapel[len(stapel)-1]
		stapel = stapel[:len(stapel)-1]
		s, e := top[0], top[1]
		x1 := o[s] * c
		y1 := a[s]
		x2 := o[e] * c
		y2 := a[e]
		dx := x2 - x1
		dy := y2 - y1
		ln := jsHypot(dx, dy)
		max := 0.0
		idx := -1
		for i := s + 1; i < e; i++ {
			x := o[i] * c
			y := a[i]
			var d float64
			if ln == 0 {
				d = jsHypot(x-x1, y-y1)
			} else {
				d = math.Abs(dy*(x-x1)-dx*(y-y1)) / ln
			}
			if d > max {
				max = d
				idx = i
			}
		}
		if idx >= 0 && max > eps {
			keep[idx] = true
			stapel = append(stapel, [2]int{s, idx}, [2]int{idx, e})
		}
	}
	return keep
}

// aufnehmen fügt ein Merkmal hinzu: a = Breiten, o = Längen (Grad)
func (kb *karteBauer) aufnehmen(a0, o0 []float64, k *merkmal) {
	minFlaeche := 3
	if !k.flaeche {
		minFlaeche = 2
	}
	if len(a0) < minFlaeche {
		return
	}
	a := slices.Clone(a0)
	o := slices.Clone(o0)
	if k.flaeche && len(a) > 3 && a[0] == a[len(a)-1] && o[0] == o[len(o)-1] {
		a = a[:len(a)-1]
		o = o[:len(o)-1]
	}
	minA, maxA := math.Inf(1), math.Inf(-1)
	minO, maxO := math.Inf(1), math.Inf(-1)
	for i := range a {
		if a[i] < minA {
			minA = a[i]
		}
		if a[i] > maxA {
			maxA = a[i]
		}
		if o[i] < minO {
			minO = o[i]
		}
		if o[i] > maxO {
			maxO = o[i]
		}
	}
	// Sehr kleine Flächen (unter etwa 20 m) sieht man ohnehin nicht
	schwelle := 0.0001
	if k.flaeche {
		schwelle = 0.0002
	}
	if math.Max(maxA-minA, maxO-minO) < schwelle {
		return
	}
	keep := vereinfache(a, o, 0.00004)
	anz := 0
	for i := range a {
		if keep[i] {
			kb.pts = append(kb.pts, int32(jsRound(a[i]*1e6)), int32(jsRound(o[i]*1e6)))
			anz++
		}
	}
	kb.off = append(kb.off, kb.off[len(kb.off)-1]+uint32(anz))
	m := uint8(0)
	if !k.flaeche {
		m = 1
	}
	kb.meta = append(kb.meta, m|uint8(k.cls<<1))
	kb.bbox = append(kb.bbox, float32(minA), float32(minO), float32(maxA), float32(maxO))
}

// ringe setzt Knotenketten (Wege als Knoten-IDs) zu geschlossenen Ringen zusammen; offene Reste werden verworfen
func ringe(wege [][]int64) [][]int64 {
	var offen [][]int64
	for _, w := range wege {
		if len(w) > 1 {
			offen = append(offen, slices.Clone(w))
		}
	}
	var ergebnis [][]int64
	for len(offen) > 0 {
		ring := offen[len(offen)-1]
		offen = offen[:len(offen)-1]
		weiter := true
		for weiter && ring[0] != ring[len(ring)-1] {
			weiter = false
			for i := 0; i < len(offen); i++ {
				w := offen[i]
				ende := ring[len(ring)-1]
				var neu []int64
				switch {
				case w[0] == ende:
					neu = append(slices.Clone(ring), w[1:]...)
				case w[len(w)-1] == ende:
					teil := slices.Clone(w[:len(w)-1])
					slices.Reverse(teil)
					neu = append(slices.Clone(ring), teil...)
				case w[len(w)-1] == ring[0]:
					neu = append(slices.Clone(w), ring[1:]...)
				case w[0] == ring[0]:
					teil := slices.Clone(w)
					slices.Reverse(teil)
					neu = append(teil, ring[1:]...)
				default:
					continue
				}
				ring = neu
				offen = append(offen[:i], offen[i+1:]...)
				weiter = true
				break
			}
		}
		if len(ring) > 3 && ring[0] == ring[len(ring)-1] {
			ergebnis = append(ergebnis, ring)
		}
	}
	return ergebnis
}

type relation struct {
	k      *merkmal
	wegIds []int64
}

// karteBauen liest die PBF-Datei und schreibt die Karte nach <netzOrdner>/karte
func karteBauen(pbfDatei, netzOrdner string, log func(string, ...any)) error {
	t0 := time.Now()
	ausgabe := filepath.Join(netzOrdner, "karte")
	if err := os.MkdirAll(ausgabe, 0o755); err != nil {
		return err
	}

	// Schritt 1: Relationen finden (Mehrfachflächen und Flüsse); die Wege ihrer Mitglieder werden gemerkt
	var relationen []relation
	benoetigteWege := map[int64]bool{}
	err := readPBF(pbfDatei, pbfOptionen{
		schluessel: karteSchluessel,
		relationen: func(id int64, tags map[string]string, mitglieder []mitglied) {
			if tags["type"] != "multipolygon" && tags["type"] != "waterway" {
				return
			}
			k := klassifiziere(tags)
			if k == nil {
				return
			}
			var wegIds []int64
			for _, m := range mitglieder {
				if m.typ == 1 && (tags["type"] == "waterway" || m.rolle == "outer" || m.rolle == "") {
					wegIds = append(wegIds, m.id)
				}
			}
			if len(wegIds) == 0 {
				return
			}
			relationen = append(relationen, relation{k: k, wegIds: wegIds})
			for _, w := range wegIds {
				benoetigteWege[w] = true
			}
		},
	})
	if err != nil {
		return err
	}
	log("Relationen: %d, benötigte Wege: %d", len(relationen), len(benoetigteWege))

	// Schritt 1b: Welche Knoten braucht die Karte? Nur die der Wege, die ein Merkmal bilden oder in einer Relation stecken.
	var bed []int64
	err = readPBF(pbfDatei, pbfOptionen{
		schluessel: karteSchluessel,
		wege: func(id int64, tags map[string]string, refs []int64) {
			if !benoetigteWege[id] && klassifiziere(tags) == nil {
				return
			}
			bed = append(bed, refs...)
		},
	})
	if err != nil {
		return err
	}
	slices.Sort(bed)
	bed = slices.Compact(bed)
	bedarf := bed
	kLat := make([]int32, len(bedarf))
	kLon := make([]int32, len(bedarf))
	gefunden := make([]bool, len(bedarf))
	log("Benötigte Knoten: %d", len(bedarf))

	// Binäre Suche nach einem benötigten und gefundenen Knoten; -1, wenn nicht
	suche := func(id int64) int {
		lo, hi := 0, len(bedarf)-1
		for lo <= hi {
			m := (lo + hi) >> 1
			w := bedarf[m]
			if w < id {
				lo = m + 1
			} else if w > id {
				hi = m - 1
			} else {
				if gefunden[m] {
					return m
				}
				return -1
			}
		}
		return -1
	}

	// Schritt 2: Knoten und Wege lesen; die Knotenketten der benötigten Wege werden gemerkt
	zeiger := 0
	wegRefs := map[int64][]int64{}
	kb := &karteBauer{off: []uint32{0}}
	err = readPBF(pbfDatei, pbfOptionen{
		schluessel: karteSchluessel,
		knoten: func(ids []int64, lat, lon []float64) {
			for i, id := range ids {
				for zeiger < len(bedarf) && bedarf[zeiger] < id {
					zeiger++
				}
				if zeiger < len(bedarf) && bedarf[zeiger] == id {
					kLat[zeiger] = int32(jsRound(lat[i] * 1e7))
					kLon[zeiger] = int32(jsRound(lon[i] * 1e7))
					gefunden[zeiger] = true
				}
			}
		},
		wege: func(id int64, tags map[string]string, refs []int64) {
			if benoetigteWege[id] {
				wegRefs[id] = slices.Clone(refs)
			}
			k := klassifiziere(tags)
			if k == nil || len(refs) < 2 {
				return
			}
			geschlossen := refs[0] == refs[len(refs)-1]
			if k.flaeche && (!geschlossen || len(refs) < 4) {
				return
			}
			var a, o []float64
			for _, r := range refs {
				idx := suche(r)
				if idx >= 0 {
					a = append(a, float64(kLat[idx])/1e7)
					o = append(o, float64(kLon[idx])/1e7)
				}
			}
			kb.aufnehmen(a, o, k)
		},
	})
	if err != nil {
		return err
	}
	anzGefunden := 0
	for _, g := range gefunden {
		if g {
			anzGefunden++
		}
	}
	log("Knoten gefunden: %d von %d, gemerkte Wege: %d", anzGefunden, len(bedarf), len(wegRefs))

	// Schritt 3: Relationen zusammensetzen: Mehrfachflächen aus geschlossenen Ringen, Flüsse als Linien
	relFlaechen, relLinien := 0, 0
	for _, r := range relationen {
		var ketten [][]int64
		for _, wid := range r.wegIds {
			refs, ok := wegRefs[wid]
			if !ok {
				continue
			}
			var teil []int64
			for _, nd := range refs {
				if suche(nd) >= 0 {
					teil = append(teil, nd)
				}
			}
			ketten = append(ketten, teil)
		}
		if r.k.flaeche {
			for _, ring := range ringe(ketten) {
				a := make([]float64, len(ring))
				o := make([]float64, len(ring))
				for i, nd := range ring {
					idx := suche(nd)
					a[i] = float64(kLat[idx]) / 1e7
					o[i] = float64(kLon[idx]) / 1e7
				}
				kb.aufnehmen(a, o, r.k)
				relFlaechen++
			}
		} else {
			for _, kette := range ketten {
				if len(kette) < 2 {
					continue
				}
				a := make([]float64, len(kette))
				o := make([]float64, len(kette))
				for i, nd := range kette {
					idx := suche(nd)
					a[i] = float64(kLat[idx]) / 1e7
					o[i] = float64(kLon[idx]) / 1e7
				}
				kb.aufnehmen(a, o, r.k)
				relLinien++
			}
		}
	}
	log("Aus Relationen: Flächen %d, Linien %d", relFlaechen, relLinien)
	log("Merkmale: %d, Punkte: %d", len(kb.meta), len(kb.pts)/2)

	if err := os.WriteFile(filepath.Join(ausgabe, "pts.i32"), bytesVon(kb.pts), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ausgabe, "off.u32"), bytesVon(kb.off), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ausgabe, "meta.u8"), kb.meta, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ausgabe, "bbox.f32"), bytesVon(kb.bbox), 0o644); err != nil {
		return err
	}
	flaechen := 0
	for _, m := range kb.meta {
		if m&1 == 0 {
			flaechen++
		}
	}
	info := map[string]any{
		"erzeugt":         time.Now().UTC().Format(time.RFC3339Nano),
		"quelle":          pbfDatei,
		"merkmale":        len(kb.meta),
		"flaechen":        flaechen,
		"linien":          len(kb.meta) - flaechen,
		"punkte":          len(kb.pts) / 2,
		"bytes":           len(kb.pts)*4 + len(kb.off)*4 + len(kb.meta) + len(kb.bbox)*4,
		"bauzeitSekunden": int(time.Since(t0).Seconds()),
	}
	infoJSON, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ausgabe, "info.json"), infoJSON, 0o644); err != nil {
		return err
	}
	log("Fertig.")
	return nil
}
