package main

// Bewertung der OpenStreetMap-Wege für Fahrräder mit Anhänger; entspricht tools/routing/tags.js.

import (
	"math"
	"strings"
)

// Art eines Wegs
const (
	klasseStrasse = 1
	klasseRadweg  = 2
	klasseWeg     = 4
)

// schluesselWege sind die Tag-Schlüssel, die der Netzbau aus den Wegen liest
var schluesselWege = map[string]bool{
	"highway": true, "oneway": true, "oneway:bicycle": true, "junction": true, "access": true, "bicycle": true,
	"vehicle": true, "motorroad": true, "service": true, "surface": true, "tracktype": true, "smoothness": true,
	"bridge": true, "tunnel": true, "cycleway": true, "cycleway:left": true, "cycleway:right": true, "cycleway:both": true,
}

// schluesselHalt sind die Schlüssel der Knoten für Orte und Halt-Regeln
var schluesselHalt = map[string]bool{"place": true, "name": true, "highway": true}

var (
	strassen            = setzeAus("primary", "primary_link", "secondary", "secondary_link", "tertiary", "tertiary_link", "unclassified", "residential", "living_street", "service", "road")
	wege                = setzeAus("track", "path", "footway", "pedestrian", "bridleway")
	keinZugang          = setzeAus("no", "private")
	unbrauchbareZufahrt = setzeAus("parking_aisle", "drive-through", "emergency_access")
	hauptstrassen       = setzeAus("primary", "primary_link", "secondary", "secondary_link", "tertiary", "tertiary_link")
	belagTabelle        = map[string]float64{
		"asphalt": 1, "concrete": 1.05, "concrete:lanes": 1.1, "concrete:plates": 1.15, "paved": 1.1, "paving_stones": 1.3, "paving_stones:lanes": 1.3,
		"sett": 1.9, "cobblestone": 1.8, "unhewn_cobblestone": 2.3, "bricks": 1.5, "compacted": 1.5, "fine_gravel": 1.6, "gravel": 2, "pebblestone": 2.2, "unpaved": 1.9,
		"ground": 2.2, "dirt": 2.2, "earth": 2.2, "mud": 3, "grass": 2.6, "sand": 3, "wood": 1.3, "metal": 1.2, "rubber": 1.1, "tartan": 1.1, "grass_paver": 1.8,
	}
	belagStandardStrasse = map[string]float64{"primary": 1, "secondary": 1, "tertiary": 1, "unclassified": 1.1, "residential": 1.05, "living_street": 1.3, "service": 1.2, "road": 1.2}
	belagTracktype       = map[string]float64{"grade1": 1.2, "grade2": 1.5, "grade3": 1.9, "grade4": 2.2, "grade5": 2.6}
)

func setzeAus(werte ...string) map[string]bool {
	m := make(map[string]bool, len(werte))
	for _, w := range werte {
		m[w] = true
	}
	return m
}

// get liefert den Wert eines Tags und ob er vorhanden ist (wie „t.key !== undefined“ in JavaScript)
func get(t map[string]string, k string) (string, bool) {
	v, ok := t[k]
	return v, ok
}

func radErlaubt(t map[string]string) bool {
	hw, _ := get(t, "highway")
	if hw == "" {
		return false
	}
	rad, _ := get(t, "bicycle")
	if rad == "no" || rad == "dismount" {
		return false
	}
	ja := rad == "yes" || rad == "designated" || rad == "permissive"
	access, _ := get(t, "access")
	vehicle, _ := get(t, "vehicle")
	if !ja && (keinZugang[access] || keinZugang[vehicle]) {
		return false
	}
	if hw == "cycleway" {
		return true
	}
	if hw == "footway" || hw == "pedestrian" || hw == "bridleway" {
		return ja
	}
	if hw == "path" || hw == "track" {
		return true
	}
	if hw == "trunk" || hw == "trunk_link" {
		return ja
	}
	if strassen[hw] {
		motorroad, _ := get(t, "motorroad")
		if motorroad == "yes" && !ja {
			return false
		}
		service, _ := get(t, "service")
		if hw == "service" && unbrauchbareZufahrt[service] {
			return false
		}
		return true
	}
	return false
}

func klasse(t map[string]string) int {
	hw, _ := get(t, "highway")
	if hw == "cycleway" {
		return klasseRadweg
	}
	if wege[hw] {
		if rad, _ := get(t, "bicycle"); rad == "designated" {
			return klasseRadweg
		}
		return klasseWeg
	}
	return klasseStrasse
}

func hauptstrasse(t map[string]string) bool {
	hw, _ := get(t, "highway")
	return hauptstrassen[hw]
}

func radwegPflicht(t map[string]string) bool {
	rad, _ := get(t, "bicycle")
	return rad == "use_sidepath"
}

// fahrtrichtung: 1 in Richtung der Wegpunkte, 2 dagegen, 3 beide
func fahrtrichtung(t map[string]string) int {
	gegenrichtungFrei := false
	for _, k := range []string{"cycleway", "cycleway:left", "cycleway:right", "cycleway:both"} {
		if v, ok := get(t, k); ok && strings.HasPrefix(v, "opposite") {
			gegenrichtungFrei = true
		}
	}
	ob, obOK := get(t, "oneway:bicycle")
	if (obOK && ob == "no") || gegenrichtungFrei {
		return 3
	}
	ow, owOK := ob, obOK
	if !owOK {
		ow, owOK = get(t, "oneway")
	}
	if owOK && (ow == "yes" || ow == "true" || ow == "1") {
		return 1
	}
	if owOK && (ow == "-1" || ow == "reverse") {
		return 2
	}
	junction, _ := get(t, "junction")
	if junction == "roundabout" && !owOK {
		return 1
	}
	return 3
}

// belagsfaktor: Rollwiderstand relativ zu Asphalt, zwischen 0,8 und 4
func belagsfaktor(t map[string]string) float64 {
	surface, _ := get(t, "surface")
	f, ok := belagTabelle[surface]
	if !ok {
		hw, _ := get(t, "highway")
		switch {
		case hw == "track":
			tt, _ := get(t, "tracktype")
			if v, ok2 := belagTracktype[tt]; ok2 {
				f = v
			} else {
				f = 1.8
			}
		case hw == "path":
			f = 1.8
		case hw == "footway" || hw == "pedestrian" || hw == "bridleway":
			f = 1.3
		case hw == "cycleway":
			f = 1.05
		default:
			if v, ok2 := belagStandardStrasse[hw]; ok2 {
				f = v
			} else {
				f = 1.1
			}
		}
	}
	s, _ := get(t, "smoothness")
	switch s {
	case "bad":
		f *= 1.2
	case "very_bad", "horrible", "very_horrible", "impassable":
		f *= 1.5
	}
	return math.Min(4, math.Max(0.8, f))
}

// bruecke: das Höhenmodell zeigt dort das Gelände, nicht den Weg
func bruecke(t map[string]string) bool {
	b, bOK := get(t, "bridge")
	tu, tuOK := get(t, "tunnel")
	return (bOK && b != "no") || (tuOK && tu != "no" && tu != "building_passage")
}

// bewertung eines befahrbaren Wegs
type bewertung struct {
	klasse   int
	fahrt    int
	belag    int // Faktor × 20, ganze Zahl
	bruecke  bool
	haupt    bool
	sidepath bool
}

// bewerteWeg liefert nil, wenn der Weg für Fahrräder nicht befahrbar ist
func bewerteWeg(t map[string]string) *bewertung {
	if !radErlaubt(t) {
		return nil
	}
	k := klasse(t)
	return &bewertung{
		klasse:   k,
		fahrt:    fahrtrichtung(t),
		belag:    int(jsRound(belagsfaktor(t) * 20)),
		bruecke:  bruecke(t),
		haupt:    k == klasseStrasse && hauptstrasse(t),
		sidepath: radwegPflicht(t),
	}
}
