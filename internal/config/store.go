package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"aioverlay/internal/atomicfile"
	"aioverlay/internal/domain"
)

type Store struct{ Path string }

func NewStore(path string) *Store { return &Store{Path: path} }

func (s *Store) LoadOrCreate() (domain.Config, error) {
	cfg, err := s.Load()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return domain.Config{}, err
	}
	cfg = Default()
	if err := s.Save(cfg); err != nil {
		return domain.Config{}, err
	}
	return cfg, nil
}

func (s *Store) Load() (domain.Config, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return domain.Config{}, err
	}
	defer f.Close()
	return Decode(f)
}

func Decode(r io.Reader) (domain.Config, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 8<<20+1))
	if err != nil {
		return domain.Config{}, err
	}
	if len(raw) > 8<<20 {
		return domain.Config{}, fmt.Errorf("configuration exceeds 8 MiB")
	}
	if err := checkJSON(raw); err != nil {
		return domain.Config{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var cfg domain.Config
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("config contains trailing JSON or invalid data")
	}
	if err := Migrate(&cfg); err != nil {
		return cfg, err
	}
	if err := Validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Store) Save(cfg domain.Config) error {
	if err := Migrate(&cfg); err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil && filepath.Dir(s.Path) != "." {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	return atomicfile.Write(s.Path, data, 0o600)
}

func (s *Store) Export(w io.Writer) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func (s *Store) Import(r io.Reader) (domain.Config, error) {
	cfg, err := Decode(r)
	if err != nil {
		return domain.Config{}, err
	}
	if err := s.Save(cfg); err != nil {
		return domain.Config{}, err
	}
	return cfg, nil
}
