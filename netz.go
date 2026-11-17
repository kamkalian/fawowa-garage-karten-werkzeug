package main

// Wegenetz für die Reichweitensuche aus OSM-PBF und SRTM-Höhen. Entspricht tools/routing/bauen.js: Kreuzungen und Wegenden sind Knoten,
// die Wege dazwischen Kanten mit Teilstücken („Legs“, höchstens etwa 120 m) mit Länge, Steigung und Richtung.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	legMaxM     = 120.0 // Teilstücke werden nach dieser Länge abgeschlossen
	glaettungM  = 400.0 // Breite des gleitenden Mittels für die Höhen
	gradientMax = 0.4
)

type orteEintrag [4]interface{} // Name, Art (city, town, village, suburb), Breite, Länge

// netzStatistik sammelt die Zahlen, die in info.json stehen
type netzStatistik struct {
	KmKlasse      map[int]float64 `json:"kmKlasse"`
	KantenLang    int             `json:"kantenLang"`
	Schleifen     int             `json:"schleifen"`
	OhneHoehe     int             `json:"ohneHoehe"`
	LegsGekappt   int             `json:"legsGekappt"`
	GradKlemmung  int             `json:"gradKlemmung"`
	WegeMitSprung int             `json:"wegeMitSprung"`
}

// netzZaehler zählt die Wege beim Einlesen
type netzZaehler struct {
	WegeGesamt        int `json:"wegeGesamt"`
	WegeBrauchbar     int `json:"wegeBrauchbar"`
	KnotenRefsFehlend int `json:"knotenRefsFehlend"`
}

var haltArten = map[string]uint8{"traffic_signals": 1, "stop": 2, "give_way": 4, "crossing": 8}
var ortsArten = map[string]bool{"city": true, "town": true, "village": true, "suburb": true}

// netzBauen liest die PBF-Datei und schreibt das Wegenetz nach ausgabe
func netzBauen(pbfDatei, hgtOrdner, ausgabe string, log func(string, ...any)) error {
	zeit0 := time.Now()
	if err := os.MkdirAll(ausgabe, 0o755); err != nil {
		return err
	}

	// ---------- 1. Knoten und Wege einlesen ----------
	var knotenIds []int64
	var knotenLat, knotenLon []int32
	wegeBegonnen := false
	letzteId := int64(0)
	haltOsm := map[int64]uint8{}
	var orte []orteEintrag
	zaehler := netzZaehler{}

	var wegStart []uint32
	var wegKnoten []int32
	var wegArt []uint8
	var wegBelag []uint8

	sucheKnoten := func(id int64) int {
		lo, hi := 0, len(knotenIds)-1
		for lo <= hi {
			mitte := (lo + hi) >> 1
			w := knotenIds[mitte]
			if w < id {
				lo = mitte + 1
			} else if w > id {
				hi = mitte - 1
			} else {
				return mitte
			}
		}
		return -1
	}

	log("Lese %s", pbfDatei)
	err := readPBF(pbfDatei, pbfOptionen{
		knotenSchluessel: schluesselHalt,
		knotenTags: func(id int64, lat, lon float64, tags map[string]string) {
			if name, ok := tags["name"]; ok && name != "" && ortsArten[tags["place"]] {
				orte = append(orte, orteEintrag{name, tags["place"], jsRound(lat*1e5) / 1e5, jsRound(lon*1e5) / 1e5})
			}
			if art, ok := haltArten[tags["highway"]]; ok {
				haltOsm[id] = art
			}
		},
		knoten: func(ids []int64, lat, lon []float64) {
			if wegeBegonnen {
				panic("Knoten nach den Wegen: die Datei ist nicht nach Typ sortiert")
			}
			for i := range ids {
				if ids[i] <= letzteId {
					panic("Knoten sind nicht nach ID sortiert")
				}
				letzteId = ids[i]
				knotenIds = append(knotenIds, ids[i])
				knotenLat = append(knotenLat, int32(jsRound(lat[i]*1e7)))
				knotenLon = append(knotenLon, int32(jsRound(lon[i]*1e7)))
			}
		},
		wege: func(id int64, tags map[string]string, refs []int64) {
			wegeBegonnen = true
			zaehler.WegeGesamt++
			if hw := tags["highway"]; hw == "" {
				return
			}
			b := bewerteWeg(tags)
			if b == nil {
				return
			}
			wegStart = append(wegStart, uint32(len(wegKnoten)))
			dazu := 0
			for _, r := range refs {
				idx := sucheKnoten(r)
				if idx < 0 {
					zaehler.KnotenRefsFehlend++
					continue
				}
				wegKnoten = append(wegKnoten, int32(idx))
				dazu++
			}
			if dazu < 2 {
				wegKnoten = wegKnoten[:len(wegKnoten)-dazu]
				wegStart = wegStart[:len(wegStart)-1]
				return
			}
			wegArt = append(wegArt, uint8(b.klasse|(b.fahrt<<3)|(boolBit(b.bruecke)<<5)|(boolBit(b.haupt)<<6)|(boolBit(b.sidepath)<<7)))
			wegBelag = append(wegBelag, uint8(b.belag))
			zaehler.WegeBrauchbar++
		},
		schluessel: schluesselWege,
	})
	if err != nil {
		return err
	}
	wegStart = append(wegStart, uint32(len(wegKnoten))) // Ende des letzten Wegs
	nWege := zaehler.WegeBrauchbar
	nKnoten := len(knotenIds)
	log("Knoten %d, Wege gesamt %d, befahrbar %d, Knotenverweise %d, fehlend %d", nKnoten, zaehler.WegeGesamt, nWege, len(wegKnoten), zaehler.KnotenRefsFehlend)

	// ---------- 2. Kreuzungen finden ----------
	nutzung := make([]uint8, nKnoten)
	for w := 0; w < nWege; w++ {
		for i := wegStart[w]; i < wegStart[w+1]; i++ {
			k := wegKnoten[i]
			if nutzung[k] < 255 {
				nutzung[k]++
			}
		}
		a := wegKnoten[wegStart[w]]
		e := wegKnoten[wegStart[w+1]-1]
		nutzung[a] = kappeU8(int(nutzung[a]) + 2)
		nutzung[e] = kappeU8(int(nutzung[e]) + 2)
	}
	log("Kreuzungen gezählt")

	// ---------- 3. Höhen, Kanten und Teilstücke ----------
	hoehenModell := neueHoehen(hgtOrdner)
	neuNr := make([]int32, nKnoten)
	for i := range neuNr {
		neuNr[i] = -1
	}
	var nLat, nLon []int32
	var eA, eB []uint32
	var eFlags, eBelag []uint8
	var eLegStart []uint32
	var eLegN []uint8
	var legLen []uint16
	var legGrad []int8
	var legKurs []uint8
	stat := netzStatistik{KmKlasse: map[int]float64{1: 0, 2: 0, 4: 0}}

	knotenNr := func(idx int) int32 {
		n := neuNr[idx]
		if n < 0 {
			n = int32(len(nLat))
			neuNr[idx] = n
			nLat = append(nLat, knotenLat[idx])
			nLon = append(nLon, knotenLon[idx])
		}
		return n
	}

	kurs := func(lat1, lon1, lat2, lon2 float64) float64 {
		dy := lat2 - lat1
		dx := (lon2 - lon1) * jsCos((lat1+lat2)/2*gradZuRad)
		b := math.Atan2(dx, dy) / gradZuRad
		if b < 0 {
			b += 360
		}
		return b
	}

	verarbeitet := 0
	for w := 0; w < nWege; w++ {
		s := int(wegStart[w])
		n := int(wegStart[w+1]) - s
		art := wegArt[w]
		klasseBits := int(art & 7)
		fahrt := int(art>>3) & 3
		istBruecke := (art>>5)&1 == 1
		haupt := int(art>>6) & 1
		sidepath := int(art>>7) & 1

		lat := make([]float64, n)
		lon := make([]float64, n)
		d := make([]float64, n)
		hoehe := make([]float64, n)
		for i := 0; i < n; i++ {
			k := wegKnoten[s+i]
			lat[i] = float64(knotenLat[k]) / 1e7
			lon[i] = float64(knotenLon[k]) / 1e7
			if i > 0 {
				dy := (lat[i] - lat[i-1]) * 110574
				dx := (lon[i] - lon[i-1]) * 111320 * jsCos((lat[i]+lat[i-1])/2*gradZuRad)
				d[i] = d[i-1] + jsHypot(dx, dy)
			}
			hoehe[i] = hoehenModell.hoehe(lat[i], lon[i])
		}
		// Lücken im Höhenmodell auffüllen (letzter gültiger Wert); ganz ohne Daten: Weg bleibt eben
		gueltig := math.NaN()
		for i := 0; i < n; i++ {
			if math.IsNaN(hoehe[i]) {
				hoehe[i] = gueltig
			} else {
				gueltig = hoehe[i]
			}
		}
		gueltig = math.NaN()
		for i := n - 1; i >= 0; i-- {
			if math.IsNaN(hoehe[i]) {
				hoehe[i] = gueltig
			} else {
				gueltig = hoehe[i]
			}
		}
		if n > 0 && math.IsNaN(hoehe[0]) {
			stat.OhneHoehe++
			for i := range hoehe {
				hoehe[i] = 0
			}
		}
		// Brücken und Tunnel: zwischen den Enden geradlinig
		if istBruecke && n > 0 && d[n-1] > 0 {
			h0 := hoehe[0]
			h1 := hoehe[n-1]
			for i := 1; i < n-1; i++ {
				hoehe[i] = h0 + (h1-h0)*d[i]/d[n-1]
			}
		}
		// Gleitendes Mittel entlang des Wegs (dreieckige Gewichte)
		if n > 2 {
			glatt := make([]float64, n)
			for i := 0; i < n; i++ {
				summe := 0.0
				gew := 0.0
				for j := i; j >= 0 && d[i]-d[j] < glaettungM; j-- {
					g := 1 - (d[i]-d[j])/glaettungM
					summe += g * hoehe[j]
					gew += g
				}
				for j := i + 1; j < n && d[j]-d[i] < glaettungM; j++ {
					g := 1 - (d[j]-d[i])/glaettungM
					summe += g * hoehe[j]
					gew += g
				}
				glatt[i] = summe / gew
			}
			// Die Enden behalten ihre Höhe, damit zusammenstoßende Wege an Kreuzungen zusammenpassen
			glatt[0] = (glatt[0] + hoehe[0]) / 2
			glatt[n-1] = (glatt[n-1] + hoehe[n-1]) / 2
			hoehe = glatt
		}

		// Kanten zwischen Kreuzungen, Teilstücke je ~120 m
		kantenStart := 0
		for i := 1; i < n; i++ {
			knoten := int(wegKnoten[s+i])
			_, hatHalt := haltOsm[knotenIds[knoten]]
			istKreuzung := nutzung[knoten] >= 2 || i == n-1 || hatHalt
			if !istKreuzung {
				continue
			}
			a := knotenNr(int(wegKnoten[s+kantenStart]))
			b := knotenNr(knoten)
			if a == b {
				stat.Schleifen++
				kantenStart = i
				continue
			}

			legsAnfang := len(legLen)
			legStart := kantenStart
			kantenLaenge := 0.0
			for j := kantenStart + 1; j <= i; j++ {
				l := d[j] - d[legStart]
				if l >= legMaxM || j == i {
					laenge := math.Max(0.1, l)
					steigung := math.Max(-gradientMax, math.Min(gradientMax, (hoehe[j]-hoehe[legStart])/laenge))
					if math.Abs((hoehe[j]-hoehe[legStart])/laenge) > gradientMax {
						stat.GradKlemmung++
					}
					richtung := kurs(lat[legStart], lon[legStart], lat[j], lon[j])
					// Sehr lange Teilstücke (über 6,5 km) passen nicht in 16 Bit: aufteilen
					for laenge > 6000 {
						legLen = append(legLen, 60000)
						legGrad = append(legGrad, int8(jsRound(steigung*200)))
						legKurs = append(legKurs, uint8(int(jsRound(richtung/360*256))&255))
						laenge -= 6000
						stat.LegsGekappt++
					}
					legLen = append(legLen, uint16(jsRound(laenge*10)))
					legGrad = append(legGrad, int8(jsRound(steigung*200)))
					legKurs = append(legKurs, uint8(int(jsRound(richtung/360*256))&255))
					kantenLaenge += l
					legStart = j
				}
			}
			nLegs := len(legLen) - legsAnfang
			if nLegs > 255 {
				stat.KantenLang++
				legLen = legLen[:legsAnfang]
				legGrad = legGrad[:legsAnfang]
				legKurs = legKurs[:legsAnfang]
				kantenStart = i
				continue
			}
			eA = append(eA, uint32(a))
			eB = append(eB, uint32(b))
			// Bits 0-1 Fahrtrichtung, 2-4 Klasse, 5 Hauptstraße, 6 Radweg-Pflicht
			eFlags = append(eFlags, uint8(fahrt|(klasseBits<<2)|(haupt<<5)|(sidepath<<6)))
			eBelag = append(eBelag, wegBelag[w])
			eLegStart = append(eLegStart, uint32(legsAnfang))
			eLegN = append(eLegN, uint8(nLegs))
			stat.KmKlasse[klasseBits] += kantenLaenge / 1000
			kantenStart = i
		}
		verarbeitet++
		if verarbeitet%400000 == 0 {
			log("Wege verarbeitet %d / %d  Knoten %d  Kanten %d", verarbeitet, nWege, len(nLat), len(eA))
		}
	}
	N := len(nLat)
	E := len(eA)
	log("Netz: %d Knoten, %d Kanten, %d Teilstücke", N, E, len(legLen))

	// ---------- 4. Nachbarschaftsliste (CSR) ----------
	grad := make([]uint32, N+1)
	for e := 0; e < E; e++ {
		grad[eA[e]+1]++
		grad[eB[e]+1]++
	}
	for i := 0; i < N; i++ {
		grad[i+1] += grad[i]
	}
	adj := make([]uint32, E*2)
	fuell := make([]uint32, N)
	copy(fuell, grad[:N])
	for e := 0; e < E; e++ {
		adj[fuell[eA[e]]] = uint32(e * 2) // verlässt a in Richtung b
		fuell[eA[e]]++
		adj[fuell[eB[e]]] = uint32(e*2 + 1) // verlässt b in Richtung a
		fuell[eB[e]]++
	}

	// Knotenart je Knoten: 0 keine, 1 Ampel, 2 Haltestelle, 3 Vorfahrt achten, 4 Überweg, 5 Kreuzung ohne Regel
	knotenTyp := make([]uint8, N)
	for idx := 0; idx < nKnoten; idx++ {
		n := neuNr[idx]
		if n < 0 {
			continue
		}
		f := haltOsm[knotenIds[idx]]
		grad3 := grad[n+1]-grad[n] >= 3
		switch {
		case f&1 != 0:
			knotenTyp[n] = 1
		case f&2 != 0:
			knotenTyp[n] = 2
		case f&4 != 0:
			knotenTyp[n] = 3
		case f&8 != 0:
			knotenTyp[n] = 4
		case grad3:
			knotenTyp[n] = 5
		default:
			knotenTyp[n] = 0
		}
	}

	// ---------- 5. Schreiben ----------
	orteJSON, err := json.Marshal(orte)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ausgabe, "orte.json"), orteJSON, 0o644); err != nil {
		return err
	}
	log("Orte: %d", len(orte))
	dateien := []struct {
		name  string
		daten []byte
	}{
		{"knoten_lat.i32", bytesVon(nLat)},
		{"knoten_lon.i32", bytesVon(nLon)},
		{"adj_start.u32", bytesVon(grad)},
		{"adj.u32", bytesVon(adj)},
		{"kante_a.u32", bytesVon(eA)},
		{"kante_b.u32", bytesVon(eB)},
		{"kante_flags.u8", bytesVon(eFlags)},
		{"kante_belag.u8", bytesVon(eBelag)},
		{"kante_leg_start.u32", bytesVon(eLegStart)},
		{"kante_leg_n.u8", bytesVon(eLegN)},
		{"leg_len.u16", bytesVon(legLen)},
		{"leg_grad.i8", bytesVon(legGrad)},
		{"leg_kurs.u8", bytesVon(legKurs)},
		{"knoten_typ.u8", knotenTyp},
	}
	gesamtBytes := 0
	for _, d := range dateien {
		if err := os.WriteFile(filepath.Join(ausgabe, d.name), d.daten, 0o644); err != nil {
			return err
		}
		gesamtBytes += len(d.daten)
	}

	// Ausdehnung des Netzes (Grad)
	minLat, maxLat, minLon, maxLon := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i := range nLat {
		a := float64(nLat[i]) / 1e7
		o := float64(nLon[i]) / 1e7
		minLat = math.Min(minLat, a)
		maxLat = math.Max(maxLat, a)
		minLon = math.Min(minLon, o)
		maxLon = math.Max(maxLon, o)
	}
	info := map[string]any{
		"erzeugt":         time.Now().UTC().Format(time.RFC3339Nano),
		"quelle":          pbfDatei,
		"knoten":          N,
		"kanten":          E,
		"teilstuecke":     len(legLen),
		"bbox":            map[string]float64{"minLat": minLat, "maxLat": maxLat, "minLon": minLon, "maxLon": maxLon},
		"kmStrasse":       math.Round(stat.KmKlasse[1]),
		"kmRadweg":        math.Round(stat.KmKlasse[2]),
		"kmWeg":           math.Round(stat.KmKlasse[4]),
		"gesamtMB":        float64(gesamtBytes) / 1048576,
		"statistik":       stat,
		"osm":             map[string]any{"wegeGesamt": zaehler.WegeGesamt, "wegeBrauchbar": zaehler.WegeBrauchbar, "knotenRefsFehlend": zaehler.KnotenRefsFehlend, "knoten": nKnoten},
		"bauzeitSekunden": int(time.Since(zeit0).Seconds()),
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

func boolBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// kappeU8 begrenzt einen Wert auf 255 (wie Math.min(255, x) in JavaScript)
func kappeU8(v int) uint8 {
	if v > 255 {
		return 255
	}
	return uint8(v)
}
