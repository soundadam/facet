package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrNotFound is returned by Resolve when no profile matches.
var ErrNotFound = errors.New("no such profile")

// Store is the profiles directory:
//
//	<Root>/profiles/<id>/profile.toml   settings
//	<Root>/profiles/<id>/user-data/     Chromium --user-data-dir
//	<Root>/profiles/<id>/run.json       present while facet runs it
//	<Root>/profiles/<id>/facet.log      supervisor log of the last run
type Store struct {
	Root string
	// Trash receives removed profiles; empty means the platform default.
	Trash string
}

// DefaultRoot is $FACET_HOME, else the user config directory's facet/
// (~/Library/Application Support/facet on macOS).
func DefaultRoot() (string, error) {
	if v := os.Getenv("FACET_HOME"); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "facet"), nil
}

func NewStore(root string) *Store { return &Store{Root: root} }

func (s *Store) Dir(id string) string         { return filepath.Join(s.Root, "profiles", id) }
func (s *Store) UserDataDir(id string) string { return filepath.Join(s.Dir(id), "user-data") }
func (s *Store) LogPath(id string) string     { return filepath.Join(s.Dir(id), "facet.log") }
func (s *Store) runPath(id string) string     { return filepath.Join(s.Dir(id), "run.json") }
func (s *Store) tomlPath(id string) string    { return filepath.Join(s.Dir(id), "profile.toml") }

// List returns every profile ordered by serial. A profile that fails to
// load is reported in the error but does not hide the others.
func (s *Store) List() ([]*Profile, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []*Profile
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := s.Load(e.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Serial < profiles[j].Serial })
	return profiles, errors.Join(errs...)
}

func (s *Store) Load(id string) (*Profile, error) {
	data, err := os.ReadFile(s.tomlPath(id))
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	var p Profile
	if _, err := toml.Decode(string(data), &p); err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	if p.ID != id {
		return nil, fmt.Errorf("profile %s: id field says %q", id, p.ID)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("profile %s: %w", id, err)
	}
	return &p, nil
}

// Resolve finds a profile by serial ("3" or "#3"), ID, or name (case-insensitive).
func (s *Store) Resolve(ref string) (*Profile, error) {
	profiles, err := s.List()
	if len(profiles) == 0 && err != nil {
		return nil, err
	}
	if n, convErr := strconv.Atoi(strings.TrimPrefix(ref, "#")); convErr == nil {
		for _, p := range profiles {
			if p.Serial == n {
				return p, nil
			}
		}
	}
	for _, p := range profiles {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

// Create assigns an ID and the next serial, then writes the profile.
func (s *Store) Create(p *Profile) error {
	profiles, err := s.List()
	if err != nil {
		return fmt.Errorf("refusing to create while a profile is unreadable: %w", err)
	}
	serial := 1
	for _, other := range profiles {
		if strings.EqualFold(other.Name, p.Name) {
			return fmt.Errorf("a profile named %q already exists (#%d)", other.Name, other.Serial)
		}
		if other.Serial >= serial {
			serial = other.Serial + 1
		}
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "profiles"), 0o700); err != nil {
		return err
	}
	for range 5 {
		id, err := newID()
		if err != nil {
			return err
		}
		if err := os.Mkdir(s.Dir(id), 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return err
		}
		p.ID, p.Serial = id, serial
		if err := s.Save(p); err != nil {
			_ = os.RemoveAll(s.Dir(id))
			return err
		}
		return nil
	}
	return errors.New("could not allocate a profile id")
}

// Save validates and atomically rewrites profile.toml.
func (s *Store) Save(p *Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# facet profile. The seed is this browser's identity: changing it\n# makes every fingerprint surface change with it.\n")
	if err := toml.NewEncoder(&buf).Encode(p); err != nil {
		return err
	}
	return writeFileAtomic(s.tomlPath(p.ID), buf.Bytes(), 0o600)
}

// Remove moves the profile directory, cookies included, to the trash.
func (s *Store) Remove(p *Profile) (string, error) {
	if state, _ := s.RunState(p.ID); state != nil {
		return "", fmt.Errorf("profile %q is running (pid %d); close it first", p.Name, state.BrowserPID)
	}
	trash := s.Trash
	if trash == "" {
		var err error
		if trash, err = defaultTrash(); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(trash, fmt.Sprintf("facet-%s-%s", sanitize(p.Name), p.ID))
	if _, err := os.Stat(dest); err == nil {
		dest += "-" + time.Now().Format("20060102-150405")
	}
	if err := os.Rename(s.Dir(p.ID), dest); err != nil {
		return "", fmt.Errorf("move to trash: %w", err)
	}
	return dest, nil
}

func defaultTrash() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash"), nil
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "Trash", "files"), nil
	}
	return filepath.Join(home, ".local", "share", "Trash", "files"), nil
}

// RunState is run.json: written by the supervisor once Chromium started.
type RunState struct {
	SupervisorPID int       `json:"supervisorPid"`
	BrowserPID    int       `json:"browserPid"`
	Bridge        string    `json:"bridge,omitempty"`
	Started       time.Time `json:"started"`
}

// RunState returns the live run state, or nil when the profile is not
// running. A run.json whose processes are gone is removed.
func (s *Store) RunState(id string) (*RunState, error) {
	data, err := os.ReadFile(s.runPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state RunState
	if err := json.Unmarshal(data, &state); err != nil || !(alive(state.BrowserPID) || alive(state.SupervisorPID)) {
		_ = os.Remove(s.runPath(id))
		return nil, nil
	}
	return &state, nil
}

// Orphaned reports a browser whose supervisor is gone: with a bridge, its
// proxied requests now fail until it is restarted.
func (r *RunState) Orphaned() bool { return !alive(r.SupervisorPID) }

func (s *Store) WriteRunState(id string, state RunState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.runPath(id), append(data, '\n'), 0o600)
}

func (s *Store) ClearRunState(id string) error {
	err := os.Remove(s.runPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func newID() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return idEncoding.EncodeToString(b), nil
}

func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ':' || r < 0x20 {
			return '_'
		}
		return r
	}, name)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
