// Package progstore provides a durable, file-backed reading-state store: an
// implementation of the opdshttp.ProgressionStore interface with companion
// storage for OPDS-PSE last-read pages, so one store persists everything a
// per-user catalog tracks about a publication (see opds.Progression and
// opds.PageStream). It uses only the standard library.
//
// Layout: one JSON record per (user, publication) at
//
//	<dir>/<user>/<publication>.json
//
// with both path segments base64url-encoded (RFC 4648 §5, unpadded), so keys
// are treated as opaque bytes and can never traverse or collide with paths.
// Records are written atomically (temp file + rename) with last-write-wins
// semantics; staleness ordering is the caller's concern (the opdshttp handler
// enforces the Progression draft's modified-based ordering before writing).
// The record format is versioned only by field addition, so stores are
// portable between servers sharing this package.
package progstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ophymx/opds"
)

// Store is a file-backed reading-state store. It is safe for concurrent use
// within a process; across processes, writes are atomic and last-write-wins.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a Store rooted at dir, creating the directory if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("progstore: %w", err)
	}
	return &Store{dir: dir}, nil
}

// record is the on-disk shape of one (user, publication) reading state.
type record struct {
	Progression  *progressionRecord `json:"progression,omitempty"`
	LastRead     int                `json:"lastRead,omitempty"`
	LastReadDate time.Time          `json:"lastReadDate,omitzero"`
}

type progressionRecord struct {
	Progression float64   `json:"progression"`
	Modified    time.Time `json:"modified"`
	DeviceID    string    `json:"deviceId,omitempty"`
	DeviceName  string    `json:"deviceName,omitempty"`
	Title       string    `json:"title,omitempty"`
	References  []string  `json:"references,omitempty"`
}

// Progression implements opdshttp.ProgressionStore. It returns
// opds.ErrNotFound when no progression has been stored.
func (s *Store) Progression(_ context.Context, user, publicationID string) (*opds.Progression, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.load(user, publicationID)
	if err != nil {
		return nil, err
	}
	if rec.Progression == nil {
		return nil, opds.ErrNotFound
	}
	p := rec.Progression
	return &opds.Progression{
		Progression: p.Progression,
		Modified:    p.Modified,
		Device:      opds.Device{ID: p.DeviceID, Name: p.DeviceName},
		Title:       p.Title,
		References:  append([]string(nil), p.References...),
	}, nil
}

// SetProgression implements opdshttp.ProgressionStore, replacing any stored
// progression and preserving the record's last-read page.
func (s *Store) SetProgression(_ context.Context, user, publicationID string, p *opds.Progression) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(user, publicationID, func(rec *record) {
		rec.Progression = &progressionRecord{
			Progression: p.Progression,
			Modified:    p.Modified,
			DeviceID:    p.Device.ID,
			DeviceName:  p.Device.Name,
			Title:       p.Title,
			References:  append([]string(nil), p.References...),
		}
	})
}

// LastRead returns the stored OPDS-PSE last-read page (1-based) and when it
// was recorded, for populating opds.PageStream.LastRead/LastReadDate. It
// returns opds.ErrNotFound when none has been stored.
func (s *Store) LastRead(_ context.Context, user, publicationID string) (page int, date time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.load(user, publicationID)
	if err != nil {
		return 0, time.Time{}, err
	}
	if rec.LastRead == 0 {
		return 0, time.Time{}, opds.ErrNotFound
	}
	return rec.LastRead, rec.LastReadDate, nil
}

// SetLastRead stores the OPDS-PSE last-read page (1-based; 0 clears it),
// preserving the record's progression. A zero date is allowed and stored
// as-is.
func (s *Store) SetLastRead(_ context.Context, user, publicationID string, page int, date time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(user, publicationID, func(rec *record) {
		rec.LastRead, rec.LastReadDate = page, date
	})
}

// load reads the record for (user, publication), mapping a missing file to
// opds.ErrNotFound.
func (s *Store) load(user, publicationID string) (record, error) {
	var rec record
	b, err := os.ReadFile(s.path(user, publicationID))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, opds.ErrNotFound
	}
	if err != nil {
		return rec, fmt.Errorf("progstore: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("progstore: corrupt record %s: %w", s.path(user, publicationID), err)
	}
	return rec, nil
}

// update applies fn to the existing record (or a fresh one) and writes it
// back atomically: the record is marshaled to a temp file in the destination
// directory, synced, and renamed into place.
func (s *Store) update(user, publicationID string, fn func(*record)) error {
	rec, err := s.load(user, publicationID)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		return err
	}
	fn(&rec)
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	path := s.path(user, publicationID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	return nil
}

func (s *Store) path(user, publicationID string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return filepath.Join(s.dir, enc([]byte(user)), enc([]byte(publicationID))+".json")
}
