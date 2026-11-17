package main

// Einleser für OpenStreetMap-PBF-Dateien (Format: wiki.openstreetmap.org/wiki/PBF_Format).
// Entspricht tools/routing/pbf.js: liest die Blöcke nacheinander und gibt Knoten, Wege und Relationen über Rückrufe weiter.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// leser ist ein Protobuf-Leser über einem Byte-Abschnitt
type leser struct {
	b    []byte
	p    int
	ende int
	feld int
	typ  int
}

func neuerLeser(b []byte, start, ende int) *leser { return &leser{b: b, p: start, ende: ende} }

func (l *leser) varint() uint64 {
	var r uint64
	var s uint
	for {
		x := l.b[l.p]
		l.p++
		r |= uint64(x&0x7f) << s
		s += 7
		if x&0x80 == 0 {
			break
		}
	}
	return r
}

func zigzag(r uint64) int64 { return int64(r>>1) ^ -int64(r&1) }

func (l *leser) zig() int64 { return zigzag(l.varint()) }

// weiter liest das nächste Feld; false am Ende des Abschnitts
func (l *leser) weiter() bool {
	if l.p >= l.ende {
		return false
	}
	k := l.varint()
	l.feld = int(k >> 3)
	l.typ = int(k & 7)
	return true
}

// bereich liest ein Feld mit Länge und liefert Anfang und Ende; danach steht der Leser dahinter
func (l *leser) bereich() (int, int) {
	n := int(l.varint())
	start := l.p
	l.p += n
	return start, start + n
}

func (l *leser) ueberspringen() error {
	switch l.typ {
	case 0:
		l.varint()
	case 1:
		l.p += 8
	case 2:
		n := int(l.varint())
		l.p += n
	case 5:
		l.p += 4
	default:
		return fmt.Errorf("unbekannter Feldtyp %d", l.typ)
	}
	return nil
}

// packedVarints liefert alle Zahlen eines gepackten Feldes
func packedVarints(b []byte, start, ende int, mitZigzag bool) []int64 {
	l := neuerLeser(b, start, ende)
	var out []int64
	for l.p < ende {
		v := l.varint()
		if mitZigzag {
			out = append(out, zigzag(v))
		} else {
			out = append(out, int64(v))
		}
	}
	return out
}

// mitglied ist ein Mitglied einer Relation
type mitglied struct {
	id    int64
	typ   int64 // 0 Knoten, 1 Weg, 2 Relation
	rolle string
}

// pbfOptionen wählt aus, was gelesen wird; nil bedeutet: nicht lesen
type pbfOptionen struct {
	knoten           func(ids []int64, lat, lon []float64) // dichte Knoten eines Blocks
	knotenTags       func(id int64, lat, lon float64, tags map[string]string)
	knotenSchluessel map[string]bool
	wege             func(id int64, tags map[string]string, refs []int64)
	schluessel       map[string]bool
	relationen       func(id int64, tags map[string]string, mitglieder []mitglied)
}

// readPBF geht die Datei einmal durch und ruft die gewählten Rückrufe auf
func readPBF(pfad string, opt pbfOptionen) error {
	f, err := os.Open(pfad)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	groesse := info.Size()
	var pos int64
	kopf := make([]byte, 4)
	for pos < groesse {
		if _, err := f.ReadAt(kopf, pos); err != nil {
			return err
		}
		kopfLaenge := int64(binary.BigEndian.Uint32(kopf))
		kopfBuf := make([]byte, kopfLaenge)
		if _, err := f.ReadAt(kopfBuf, pos+4); err != nil {
			return err
		}
		kl := neuerLeser(kopfBuf, 0, len(kopfBuf))
		typ := ""
		var datenLaenge int64
		for kl.weiter() {
			if kl.feld == 1 && kl.typ == 2 {
				a, e := kl.bereich()
				typ = string(kopfBuf[a:e])
			} else if kl.feld == 3 && kl.typ == 0 {
				datenLaenge = int64(kl.varint())
			} else if err := kl.ueberspringen(); err != nil {
				return err
			}
		}
		blob := make([]byte, datenLaenge)
		if _, err := f.ReadAt(blob, pos+4+kopfLaenge); err != nil {
			return err
		}
		pos += 4 + kopfLaenge + datenLaenge
		if typ != "OSMData" {
			continue
		}
		daten, err := blobDaten(blob)
		if err != nil {
			return err
		}
		if err := primitivBlock(daten, opt); err != nil {
			return err
		}
	}
	return nil
}

// blobDaten liefert die Nutzdaten eines Blobs (roh oder zlib-komprimiert)
func blobDaten(blob []byte) ([]byte, error) {
	bl := neuerLeser(blob, 0, len(blob))
	var daten []byte
	for bl.weiter() {
		if bl.feld == 1 && bl.typ == 2 {
			a, e := bl.bereich()
			daten = blob[a:e]
		} else if bl.feld == 3 && bl.typ == 2 {
			a, e := bl.bereich()
			r, err := zlib.NewReader(bytes.NewReader(blob[a:e]))
			if err != nil {
				return nil, err
			}
			daten, err = io.ReadAll(r)
			r.Close()
			if err != nil {
				return nil, err
			}
		} else if err := bl.ueberspringen(); err != nil {
			return nil, err
		}
	}
	if daten == nil {
		return nil, errors.New("Block ohne Daten (andere Kompression?)")
	}
	return daten, nil
}

// primitivBlock verarbeitet die Gruppen eines Datenblocks (Knoten, Wege, Relationen)
func primitivBlock(daten []byte, opt pbfOptionen) error {
	pl := neuerLeser(daten, 0, len(daten))
	var strBereich [2]int
	hatStrings := false
	granularitaet := int64(100)
	var latOffset, lonOffset int64
	var gruppen [][2]int
	for pl.weiter() {
		switch {
		case pl.feld == 1 && pl.typ == 2:
			a, e := pl.bereich()
			strBereich = [2]int{a, e}
			hatStrings = true
		case pl.feld == 2 && pl.typ == 2:
			a, e := pl.bereich()
			gruppen = append(gruppen, [2]int{a, e})
		case pl.feld == 17 && pl.typ == 0:
			granularitaet = int64(pl.varint())
		case pl.feld == 19 && pl.typ == 0:
			latOffset = pl.zig()
		case pl.feld == 20 && pl.typ == 0:
			lonOffset = pl.zig()
		default:
			if err := pl.ueberspringen(); err != nil {
				return err
			}
		}
	}
	var strings []string
	stringsLaden := func() []string {
		if strings != nil || !hatStrings {
			return strings
		}
		strings = []string{}
		sl := neuerLeser(daten, strBereich[0], strBereich[1])
		for sl.weiter() {
			if sl.feld == 1 && sl.typ == 2 {
				a, e := sl.bereich()
				strings = append(strings, string(daten[a:e]))
			} else {
				sl.ueberspringen()
			}
		}
		return strings
	}

	for _, g := range gruppen {
		gl := neuerLeser(daten, g[0], g[1])
		for gl.weiter() {
			switch {
			case gl.feld == 2 && gl.typ == 2 && (opt.knoten != nil || opt.knotenTags != nil):
				if err := dichteKnoten(daten, gl, granularitaet, latOffset, lonOffset, opt, stringsLaden); err != nil {
					return err
				}
			case gl.feld == 3 && gl.typ == 2 && opt.wege != nil:
				if err := weg(daten, gl, opt.schluessel, opt.wege, stringsLaden); err != nil {
					return err
				}
			case gl.feld == 4 && gl.typ == 2 && opt.relationen != nil:
				if err := relationLesen(daten, gl, opt.schluessel, opt.relationen, stringsLaden); err != nil {
					return err
				}
			default:
				if err := gl.ueberspringen(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func dichteKnoten(daten []byte, gl *leser, gran, latOff, lonOff int64, opt pbfOptionen, stringsLaden func() []string) error {
	a, e := gl.bereich()
	dl := neuerLeser(daten, a, e)
	var idB, latB, lonB, kvB [2]int
	var hatId, hatLat, hatLon, hatKv bool
	for dl.weiter() {
		switch {
		case dl.feld == 1 && dl.typ == 2:
			idB[0], idB[1] = dl.bereich()
			hatId = true
		case dl.feld == 10 && dl.typ == 2:
			kvB[0], kvB[1] = dl.bereich()
			hatKv = true
		case dl.feld == 8 && dl.typ == 2:
			latB[0], latB[1] = dl.bereich()
			hatLat = true
		case dl.feld == 9 && dl.typ == 2:
			lonB[0], lonB[1] = dl.bereich()
			hatLon = true
		default:
			if err := dl.ueberspringen(); err != nil {
				return err
			}
		}
	}
	if !hatId || !hatLat || !hatLon {
		return nil
	}
	ids := packedVarints(daten, idB[0], idB[1], true)
	lats := packedVarints(daten, latB[0], latB[1], true)
	lons := packedVarints(daten, lonB[0], lonB[1], true)
	n := len(ids)
	idsOut := make([]int64, n)
	latOut := make([]float64, n)
	lonOut := make([]float64, n)
	var i0, a0, o0 int64
	for i := 0; i < n; i++ {
		i0 += ids[i]
		a0 += lats[i]
		o0 += lons[i]
		idsOut[i] = i0
		latOut[i] = 1e-9 * float64(latOff+gran*a0)
		lonOut[i] = 1e-9 * float64(lonOff+gran*o0)
	}
	if opt.knoten != nil {
		opt.knoten(idsOut, latOut, lonOut)
	}
	if opt.knotenTags != nil && hatKv {
		st := stringsLaden()
		kv := neuerLeser(daten, kvB[0], kvB[1])
		for i := 0; i < n && kv.p < kvB[1]; i++ {
			var tags map[string]string
			for kv.p < kvB[1] {
				k := kv.varint()
				if k == 0 {
					break
				}
				v := kv.varint()
				if opt.knotenSchluessel[st[k]] {
					if tags == nil {
						tags = map[string]string{}
					}
					tags[st[k]] = st[v]
				}
			}
			if tags != nil {
				opt.knotenTags(idsOut[i], latOut[i], lonOut[i], tags)
			}
		}
	}
	return nil
}

func weg(daten []byte, gl *leser, schluessel map[string]bool, rueck func(int64, map[string]string, []int64), stringsLaden func() []string) error {
	a, e := gl.bereich()
	wl := neuerLeser(daten, a, e)
	var id uint64
	var keyB, valB, refB [2]int
	var hatKey, hatRef bool
	for wl.weiter() {
		switch {
		case wl.feld == 1 && wl.typ == 0:
			id = wl.varint()
		case wl.feld == 2 && wl.typ == 2:
			keyB[0], keyB[1] = wl.bereich()
			hatKey = true
		case wl.feld == 3 && wl.typ == 2:
			valB[0], valB[1] = wl.bereich()
		case wl.feld == 8 && wl.typ == 2:
			refB[0], refB[1] = wl.bereich()
			hatRef = true
		default:
			if err := wl.ueberspringen(); err != nil {
				return err
			}
		}
	}
	if !hatKey || !hatRef {
		return nil
	}
	s := stringsLaden()
	tags := map[string]string{}
	kl := neuerLeser(daten, keyB[0], keyB[1])
	vl := neuerLeser(daten, valB[0], valB[1])
	for kl.p < keyB[1] {
		k := s[kl.varint()]
		v := s[vl.varint()]
		if schluessel[k] {
			tags[k] = v
		}
	}
	refs := packedVarints(daten, refB[0], refB[1], true)
	var r0 int64
	for i := range refs {
		r0 += refs[i]
		refs[i] = r0
	}
	rueck(int64(id), tags, refs)
	return nil
}

func relationLesen(daten []byte, gl *leser, schluessel map[string]bool, rueck func(int64, map[string]string, []mitglied), stringsLaden func() []string) error {
	a, e := gl.bereich()
	rl := neuerLeser(daten, a, e)
	var id uint64
	var keyB, valB, rolB, memB, typB [2]int
	var hatKey, hatVal, hatRol, hatMem, hatTyp bool
	for rl.weiter() {
		switch {
		case rl.feld == 1 && rl.typ == 0:
			id = rl.varint()
		case rl.feld == 2 && rl.typ == 2:
			keyB[0], keyB[1] = rl.bereich()
			hatKey = true
		case rl.feld == 3 && rl.typ == 2:
			valB[0], valB[1] = rl.bereich()
			hatVal = true
		case rl.feld == 8 && rl.typ == 2:
			rolB[0], rolB[1] = rl.bereich()
			hatRol = true
		case rl.feld == 9 && rl.typ == 2:
			memB[0], memB[1] = rl.bereich()
			hatMem = true
		case rl.feld == 10 && rl.typ == 2:
			typB[0], typB[1] = rl.bereich()
			hatTyp = true
		default:
			if err := rl.ueberspringen(); err != nil {
				return err
			}
		}
	}
	s := stringsLaden()
	tags := map[string]string{}
	if hatKey && hatVal {
		kl := neuerLeser(daten, keyB[0], keyB[1])
		vl := neuerLeser(daten, valB[0], valB[1])
		for kl.p < keyB[1] {
			k := s[kl.varint()]
			v := s[vl.varint()]
			if schluessel[k] {
				tags[k] = v
			}
		}
	}
	var mitglieder []mitglied
	if hatMem && hatRol && hatTyp {
		mid := packedVarints(daten, memB[0], memB[1], true)
		mrol := packedVarints(daten, rolB[0], rolB[1], false)
		mtyp := packedVarints(daten, typB[0], typB[1], false)
		var m0 int64
		for i := range mid {
			m0 += mid[i]
			mitglieder = append(mitglieder, mitglied{id: m0, typ: mtyp[i], rolle: s[mrol[i]]})
		}
	}
	rueck(int64(id), tags, mitglieder)
	return nil
}
