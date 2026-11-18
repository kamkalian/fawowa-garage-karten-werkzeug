//go:build !windows

package main

import "errors"

// Ohne Windows gibt es keine DPAPI: Geheimnisse werden nicht gespeichert.
func Verschluesseln(klartext string) (string, error) {
	return "", errors.New("Speichern von Passwörtern ist nur unter Windows möglich")
}

func Entschluesseln(geheim string) (string, error) {
	return "", errors.New("Speichern von Passwörtern ist nur unter Windows möglich")
}
