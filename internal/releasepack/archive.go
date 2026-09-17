// Package releasepack creates reproducible archives from explicitly selected files.
package releasepack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type File struct {
	Name string
	Path string
	Data []byte
	Mode os.FileMode
}
type ArchiveReport struct {
	Archive         string `json:"archive"`
	SHA256          string `json:"sha256"`
	Bytes           int64  `json:"bytes"`
	Files           int    `json:"files"`
	CRC             string `json:"crc"`
	Manifest        string `json:"manifest"`
	CleanExtraction string `json:"clean_extraction"`
	SourceTests     string `json:"source_tests,omitempty"`
	SourceVet       string `json:"source_vet,omitempty"`
	ReproducesEXE   string `json:"reproduces_windows_exe,omitempty"`
}

func SafeName(name string) bool {
	return name != "" && name == filepath.ToSlash(name) && !strings.ContainsAny(name, "\\\x00:\r\n") && filepath.IsLocal(name) && !strings.HasPrefix(name, "/") && filepath.ToSlash(filepath.Clean(name)) == name
}
func (f File) reader() (io.ReadCloser, error) {
	if f.Data != nil {
		return io.NopCloser(bytes.NewReader(f.Data)), nil
	}
	return os.Open(f.Path)
}
func Manifest(files []File) ([]byte, error) {
	files = append([]File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var out strings.Builder
	seen := map[string]bool{}
	for _, f := range files {
		if !SafeName(f.Name) || seen[f.Name] || f.Name == "MANIFEST.sha256" {
			return nil, fmt.Errorf("invalid/duplicate release entry: %s", f.Name)
		}
		seen[f.Name] = true
		r, err := f.reader()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, r)
		r.Close()
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&out, "%x  %s\n", h.Sum(nil), f.Name)
	}
	return []byte(out.String()), nil
}
func Write(path string, files []File) error {
	manifest, err := Manifest(files)
	if err != nil {
		return err
	}
	files = append(append([]File(nil), files...), File{Name: "MANIFEST.sha256", Data: manifest, Mode: 0644})
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	writer := zip.NewWriter(f)
	for _, entry := range files {
		header := zip.FileHeader{Name: "AIOverlay-go/" + entry.Name, Method: zip.Deflate, Modified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
		mode := entry.Mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		dst, err := writer.CreateHeader(&header)
		if err != nil {
			writer.Close()
			return err
		}
		src, err := entry.reader()
		if err != nil {
			writer.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if err != nil {
			writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// VerifyExtract reads every byte (zip's CRC is verified by Read), enforces complete
// manifest coverage, and rejects traversal, duplicate entries and symlinks.
func VerifyExtract(path, destination string) (ArchiveReport, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer z.Close()
	if len(z.File) > 10000 {
		return ArchiveReport{}, fmt.Errorf("excessive archive entries")
	}
	entries := map[string]*zip.File{}
	var manifest []byte
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "AIOverlay-go/") {
			return ArchiveReport{}, fmt.Errorf("unexpected archive root")
		}
		name := strings.TrimPrefix(file.Name, "AIOverlay-go/")
		if !SafeName(name) || file.FileInfo().Mode()&os.ModeSymlink != 0 || file.FileInfo().IsDir() || entries[name] != nil {
			return ArchiveReport{}, fmt.Errorf("unsafe/duplicate ZIP entry")
		}
		entries[name] = file
		if name == "MANIFEST.sha256" {
			if file.UncompressedSize64 > 8<<20 {
				return ArchiveReport{}, fmt.Errorf("manifest too large")
			}
			r, err := file.Open()
			if err != nil {
				return ArchiveReport{}, err
			}
			manifest, err = io.ReadAll(r)
			r.Close()
			if err != nil {
				return ArchiveReport{}, err
			}
		}
	}
	if len(manifest) == 0 {
		return ArchiveReport{}, fmt.Errorf("manifest missing")
	}
	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || !SafeName(name) || name == "MANIFEST.sha256" || listed[name] != "" {
			return ArchiveReport{}, fmt.Errorf("invalid manifest entry")
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return ArchiveReport{}, err
		}
		listed[name] = sum
	}
	if len(listed)+1 != len(entries) {
		return ArchiveReport{}, fmt.Errorf("manifest coverage mismatch")
	}
	for name, file := range entries {
		if name != "MANIFEST.sha256" && listed[name] == "" {
			return ArchiveReport{}, fmt.Errorf("unmanifested file")
		}
		if file.UncompressedSize64 > 512<<20 {
			return ArchiveReport{}, fmt.Errorf("oversized archive file")
		}
		target := filepath.Join(destination, "AIOverlay-go", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return ArchiveReport{}, err
		}
		src, err := file.Open()
		if err != nil {
			return ArchiveReport{}, err
		}
		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			src.Close()
			return ArchiveReport{}, err
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(dst, h), src)
		src.Close()
		closeErr := dst.Close()
		if err != nil {
			return ArchiveReport{}, err
		}
		if closeErr != nil {
			return ArchiveReport{}, closeErr
		}
		if name != "MANIFEST.sha256" && hex.EncodeToString(h.Sum(nil)) != listed[name] {
			return ArchiveReport{}, fmt.Errorf("manifest hash mismatch: %s", name)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return ArchiveReport{}, err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return ArchiveReport{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return ArchiveReport{}, err
	}
	return ArchiveReport{Archive: filepath.Base(path), SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: st.Size(), Files: len(entries), CRC: "PASS", Manifest: "PASS", CleanExtraction: "PASS"}, nil
}
