package main

// Hilfsfunktionen, die sich wie in JavaScript verhalten (damit die Ausgabe mit der Node-Version Ã¼bereinstimmt).

import (
	"math"
	"unsafe"
)

// jsRound entspricht Math.round: halbe Werte gehen nach oben (-2,5 wird zu -2)
func jsRound(x float64) float64 {
	return math.Floor(x + 0.5)
}

// bytesVon gibt die Bytes eines Zahlenfeldes in der Rechner-Reihenfolge (little-endian) zurÃ¼ck, wie Buffer.from(typedArray.buffer)
func bytesVon[T any](x []T) []byte {
	if len(x) == 0 {
		return nil
	}
	var z T
	groesse := int(unsafe.Sizeof(z)) * len(x)
	return unsafe.Slice((*byte)(unsafe.Pointer(&x[0])), groesse)
}

// jsHypot rechnet wie Math.hypot in V8 (Node): Skalierung mit dem grÃ¶ÃŸten Betrag und Kahan-Summe der Quadrate
func jsHypot(werte ...float64) float64 {
	max := 0.0
	for _, w := range werte {
		a := math.Abs(w)
		if a > max {
			max = a
		}
	}
	if max == 0 {
		return 0
	}
	sum := 0.0
	kompensation := 0.0
	for _, w := range werte {
		n := math.Abs(w) / max
		summand := n*n - kompensation
		vorlaeufig := sum + summand
		kompensation = (vorlaeufig - sum) - summand
		sum = vorlaeufig
	}
	return math.Sqrt(sum) * max
}

// piJS und gradZuRad sind Variablen (keine Konstanten), damit Go die Formeln zur Laufzeit in doppelter Genauigkeit rechnet wie JavaScript.
// Konstanten würden mit beliebiger Genauigkeit gerechnet und in der letzten Stelle abweichen.
var piJS = math.Pi
var gradZuRad = piJS / 180
