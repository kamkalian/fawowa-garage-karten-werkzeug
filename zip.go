package main

// ZIP-Datei mit dem Wegenetz und der Karte; die Anwendung auf dem Server packt sie wieder aus (lib/zip.js, lib/worker.js).

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// zippen packt alle Dateien von ordner (ohne den Ordner selbst) in ziel
func zippen(ordner, ziel string) error {
	var gesamt int64
	err := filepath.WalkDir(ordner, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			gesamt += info.Size()
		}
		return nil
	})
	if err != nil {
		return err
	}

	teil := ziel + ".teil"
	out, err := os.Create(teil)
	if err != nil {
		return err
	}
	z := zip.NewWriter(out)
	var geschrieben int64
	err = filepath.WalkDir(ordner, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(ordner, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		kopf, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		kopf.Name = filepath.ToSlash(rel)
		kopf.Method = zip.Deflate
		w, err := z.CreateHeader(kopf)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		buf := make([]byte, 1<<20)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return werr
				}
				geschrieben += int64(n)
				fortschrittZeile("Packen", float64(geschrieben)/float64(maxInt64(gesamt, 1)), mb(geschrieben)+" von "+mb(gesamt))
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
		return nil
	})
	if err != nil {
		z.Close()
		out.Close()
		return err
	}
	if err := z.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	fortschrittZeile("Packen", 1, mb(geschrieben))
	fmt.Println()
	return os.Rename(teil, ziel)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
