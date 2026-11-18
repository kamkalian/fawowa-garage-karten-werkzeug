package main

// Dateiauswahl für die Schlüsseldatei: der Windows-Dateidialog liefert den vollen Pfad (ein Browser gibt ihn nicht heraus).

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// schluesselDialog zeigt den Windows-Dateidialog und liefert den gewählten Pfad (leer, wenn abgebrochen)
func schluesselDialog() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("Die Dateiauswahl gibt es nur unter Windows. Bitte den Pfad eintragen.")
	}
	skript := `Add-Type -AssemblyName System.Windows.Forms; ` +
		`$d = New-Object System.Windows.Forms.OpenFileDialog; ` +
		`$d.Title = 'Schluesseldatei waehlen'; ` +
		`$d.InitialDirectory = (Join-Path $env:USERPROFILE '.ssh'); ` +
		`if ($d.ShowDialog() -eq 'OK') { Write-Output $d.FileName }`
	out, err := exec.Command("powershell", "-NoProfile", "-STA", "-Command", skript).Output()
	if err != nil {
		return "", errors.New("Der Dateiauswahl-Dialog konnte nicht geöffnet werden.")
	}
	return strings.TrimSpace(string(out)), nil
}
