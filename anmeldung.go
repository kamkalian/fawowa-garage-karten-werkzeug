package main

import "strings"

// anmeldeFehlerText macht den Fehler bei der Anmeldung verständlich
func anmeldeFehlerText(err error) string {
	if err != nil && strings.Contains(err.Error(), "unable to authenticate") {
		return "Der Server lehnt die Anmeldung ab. Benutzer und Schlüssel müssen zusammenpassen: Der Schlüssel muss für genau diesen Benutzer auf dem Server hinterlegt sein. Bitte den Benutzernamen prüfen."
	}
	return err.Error()
}
