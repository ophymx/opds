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
// with both path segments encoded as unpadded base32 (RFC 4648 §6) — a
// single-case alphabet, so distinct keys stay distinct even on
// case-insensitive filesystems, and keys are treated as opaque bytes that can
// never traverse or collide with paths. A key whose encoding would approach
// filesystem name-length limits is stored under its SHA-256 digest instead,
// marked with a "@" prefix (outside the base32 alphabet), so keys of any
// length work. Records are written atomically: temp file, fsync, rename, and
// a best-effort fsync of the containing directory. The record format is
// versioned only by field addition, so stores are portable between servers
// sharing this package.
//
// A Store is safe for concurrent use within one process. Run a single server
// process per store directory: writes are last-write-wins at whole-record
// granularity, so two processes updating the same record concurrently could
// interleave progression and last-read updates (and the opdshttp handler's
// staleness ordering is only serialized in-process).
//
// The last-read half is the persistence slot for OPDS-PSE server-side resume:
// the application's page-serving path calls SetLastRead as page fetches
// arrive, and its Source reads LastRead when populating
// opds.PageStream.LastRead/LastReadDate for the authenticated user (see
// opdshttp.User). The library itself never writes it — per-page tracking
// policy belongs to the application.
package progstore

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ophymx/opds"
)

// Store is a file-backed reading-state store. See the package documentation
// for the on-disk layout and concurrency model.
type Store struct {
	dir string
	// mu stripes record-level read-modify-write by (user, publication).
	mu [16]sync.Mutex
}

// New returns a Store rooted at dir, creating the directory if needed and
// sweeping temp files left behind by a crashed process.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("progstore: %w", err)
	}
	// Orphaned temp files are only ever left by a write that died between
	// create and rename; with a single writing process none are in flight now.
	if orphans, err := filepath.Glob(filepath.Join(dir, "*", ".tmp-*")); err == nil {
		for _, o := range orphans {
			os.Remove(o)
		}
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

// errCorruptRecord tags a record that exists but does not parse. Reads
// surface it so the damage is visible; writes treat the record as absent and
// heal it with the next update.
var errCorruptRecord = errors.New("progstore: corrupt record")

// Progression implements opdshttp.ProgressionStore. It returns
// opds.ErrNotFound when no progression has been stored.
func (s *Store) Progression(_ context.Context, user, publicationID string) (*opds.Progression, error) {
	mu := s.lock(user, publicationID)
	mu.Lock()
	defer mu.Unlock()
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
	mu := s.lock(user, publicationID)
	mu.Lock()
	defer mu.Unlock()
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
	mu := s.lock(user, publicationID)
	mu.Lock()
	defer mu.Unlock()
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
	mu := s.lock(user, publicationID)
	mu.Lock()
	defer mu.Unlock()
	return s.update(user, publicationID, func(rec *record) {
		rec.LastRead, rec.LastReadDate = page, date
	})
}

func (s *Store) lock(user, publicationID string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(user))
	h.Write([]byte{0})
	h.Write([]byte(publicationID))
	return &s.mu[h.Sum32()%uint32(len(s.mu))]
}

// load reads the record for (user, publication), mapping a missing file to
// opds.ErrNotFound and an unparseable one to errCorruptRecord.
func (s *Store) load(user, publicationID string) (record, error) {
	var rec record
	path := s.path(user, publicationID)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, opds.ErrNotFound
	}
	if err != nil {
		return rec, fmt.Errorf("progstore: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return record{}, fmt.Errorf("%w: %s: %v", errCorruptRecord, path, err)
	}
	return rec, nil
}

// update applies fn to the existing record (or a fresh one — a corrupt record
// is discarded and healed by the write) and writes it back atomically: the
// record is marshaled to a temp file in the destination directory, synced,
// renamed into place, and the directory entry is synced best-effort.
func (s *Store) update(user, publicationID string, fn func(*record)) error {
	rec, err := s.load(user, publicationID)
	if err != nil && !errors.Is(err, opds.ErrNotFound) && !errors.Is(err, errCorruptRecord) {
		return err
	}
	fn(&rec)
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("progstore: %w", err)
	}
	path := s.path(user, publicationID)
	dir := filepath.Dir(path)
	newDir := false
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		newDir = true
	}
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
	// Sync the directory entries so the rename (and a first-time directory
	// creation) survive power loss. Best-effort: not every platform supports
	// syncing directories.
	syncDir(dir)
	if newDir {
		syncDir(s.dir)
	}
	return nil
}

func syncDir(path string) {
	if d, err := os.Open(path); err == nil {
		d.Sync()
		d.Close()
	}
}

// base32enc is unpadded RFC 4648 base32: its single-case alphabet keeps
// distinct keys distinct on case-insensitive filesystems.
var base32enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// maxEncodedName caps a plainly encoded path segment (well under common
// 255-byte filename limits, with margin for the ".json" suffix and stricter
// filesystems); longer keys are stored under their digest.
const maxEncodedName = 128

// encodeKey maps an opaque key to a filesystem-safe name: base32 of the key
// itself, or "@" plus base32 of its SHA-256 digest when the plain encoding
// would exceed maxEncodedName. The "@" is outside the base32 alphabet, so the
// two forms cannot collide.
func encodeKey(key string) string {
	if enc := base32enc.EncodeToString([]byte(key)); len(enc) <= maxEncodedName {
		return enc
	}
	sum := sha256.Sum256([]byte(key))
	return "@" + base32enc.EncodeToString(sum[:])
}

func (s *Store) path(user, publicationID string) string {
	return filepath.Join(s.dir, encodeKey(user), encodeKey(publicationID)+".json")
}
