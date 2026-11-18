//go:build windows

package main

// Verschlüsselung für Passwörter und Passphrasen mit Windows-DPAPI: nur der angemeldete Benutzer kann sie wieder entschlüsseln.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	crypt32                = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func neuerBlob(daten []byte) dataBlob {
	if len(daten) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(daten)), pbData: &daten[0]}
}

func blobBytes(b dataBlob) []byte {
	if b.cbData == 0 {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

// Verschluesseln liefert einen Text (Base64), der nur unter diesem Windows-Benutzer entschlüsselt werden kann
func Verschluesseln(klartext string) (string, error) {
	in := []byte(klartext)
	eingabe := neuerBlob(in)
	var ausgabe dataBlob
	r, _, err := procCryptProtectData.Call(uintptr(unsafe.Pointer(&eingabe)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&ausgabe)))
	if r == 0 {
		return "", fmt.Errorf("Verschlüsselung fehlgeschlagen: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(ausgabe.pbData)))
	return base64.StdEncoding.EncodeToString(blobBytes(ausgabe)), nil
}

// Entschluesseln kehrt Verschluesseln um
func Entschluesseln(geheim string) (string, error) {
	roh, err := base64.StdEncoding.DecodeString(geheim)
	if err != nil {
		return "", errors.New("gespeichertes Geheimnis ist beschädigt")
	}
	eingabe := neuerBlob(roh)
	var ausgabe dataBlob
	r, _, err := procCryptUnprotectData.Call(uintptr(unsafe.Pointer(&eingabe)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&ausgabe)))
	if r == 0 {
		return "", fmt.Errorf("Entschlüsselung fehlgeschlagen (anderer Benutzer oder Rechner): %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(ausgabe.pbData)))
	return string(blobBytes(ausgabe)), nil
}
