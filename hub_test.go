package main

import "testing"

// Beim Verbinden (Neuladen der Seite) bekommt die neue Verbindung die letzten Ereignisse
func TestHubSpieltLetzteEreignisseAb(t *testing.T) {
	h := neuerHub()
	h.Senden(Ereignis{Art: "protokoll", Text: "eins"})
	h.Senden(Ereignis{Art: "fortschritt", Schritt: "hochladen", Anteil: 0.5})
	k := h.Abonnieren()
	for i, erwartet := range []string{"eins", ""} {
		select {
		case e := <-k:
			if i == 0 && e.Text != erwartet {
				t.Fatalf("erstes Ereignis %q statt %q", e.Text, erwartet)
			}
		default:
			t.Fatalf("Ereignis %d fehlt nach dem Verbinden", i)
		}
	}
}
