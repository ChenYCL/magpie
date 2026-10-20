package presets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
)

// The store: presets.json in magpie's config folder, the user's presets,
// the edits of the built-ins, and which preset each agent gets.
//
//	{
//	  "custom":   [Preset…],                  // the user's, builtin false
//	  "builtin":  {"standard": {Patch…}},      // an edit of a built-in, by id
//	  "agents":   {"claude-code": "standard"}, // the preset an agent gets
//	  "default":  "standard"                   // the fallback, too
//	}
//
// A built-in edited keeps its identity and its patches' order: the edit
// replaces what it has, and Reset puts the shipped one back. Nothing here
// is imported by internal/gui's core, so the page reads it through
// internal/presets alone.

// File is the store's file, by name in magpie's config folder.
const File = "presets.json"

// Path is the store's file.
func Path() string { return filepath.Join(appdir.Config(), File) }

// Store is what presets.json holds.
type Store struct {
	Custom  []Preset          `json:"custom,omitempty"`
	Builtin map[string]Preset `json:"builtin,omitempty"`
	Agents  map[string]string `json:"agents,omitempty"`
	Default string            `json:"default,omitempty"`
}

var (
	mu    sync.Mutex
	cache *Store
)

// Load is the store as it is on disk, read once and kept until Save. A
// file that can't be read is an empty store: a damaged presets.json must
// not stop a request from being served.
func Load() Store {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() Store {
	if cache != nil {
		return *cache
	}
	s := Store{}
	b, err := os.ReadFile(Path())
	if err == nil && len(b) > 0 {
		var got Store
		if json.Unmarshal(b, &got) == nil {
			s = got
		}
	}
	cache = &s
	return *cache
}

// Read is the store as it is on disk now, the cache put aside.
func Read() (Store, error) {
	mu.Lock()
	defer mu.Unlock()
	cache = nil
	return load(), nil
}

// Save writes the store, the cache with it.
func Save(s Store) error {
	mu.Lock()
	defer mu.Unlock()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	cache = &s
	return nil
}

// All is every preset the window lists: the built-ins as edited, then the
// user's, in their cards' order.
func All() []Preset {
	mu.Lock()
	defer mu.Unlock()
	return allOf(load())
}

// allOf is All for a store already read: the writers hold the lock and
// must not take it again.
func allOf(s Store) []Preset {
	out := make([]Preset, 0, len(builtins)+len(s.Custom))
	for _, b := range builtins {
		p := b.Clone()
		if e, ok := s.Builtin[b.ID]; ok {
			// the edit keeps what makes it a built-in
			edit := e.Clone()
			edit.ID, edit.Builtin, edit.Order, edit.Name = p.ID, true, p.Order, p.Name
			if edit.Sub == "" {
				edit.Sub = p.Sub
			}
			if edit.About == "" {
				edit.About = p.About
			}
			if edit.Version == "" {
				edit.Version = p.Version
			}
			p = edit
		}
		out = append(out, p)
	}
	out = append(out, s.Custom...)
	return Sorted(out)
}

// Get is the preset with id, and whether there is one.
func Get(id string) (Preset, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// For is the preset an agent gets: the one set for it, else the default,
// else the first built-in — nil only when magpie has no presets at all.
func For(agent string) (Preset, bool) {
	s := Load()
	list := All()
	if len(list) == 0 {
		return Preset{}, false
	}
	byID := map[string]Preset{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if id := s.Agents[agent]; id != "" {
		if p, ok := byID[id]; ok && !p.Disabled {
			return p, true
		}
	}
	if p, ok := byID[s.Default]; ok && !p.Disabled {
		return p, true
	}
	for _, p := range list {
		if !p.Disabled {
			return p, true
		}
	}
	return Preset{}, false
}

// Bind sets the preset an agent gets; "" falls back to the default.
func Bind(agent, id string) error {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if id != "" {
		found := false
		for _, p := range allOf(s) {
			if p.ID == id && !p.Disabled {
				found = true
				break
			}
		}
		if !found {
			return errNoPreset(id)
		}
	}
	if s.Agents == nil {
		s.Agents = map[string]string{}
	}
	if id == "" {
		delete(s.Agents, agent)
	} else {
		s.Agents[agent] = id
	}
	cache = nil
	return write(s)
}

// SetDefault sets the preset every agent without one of its own gets.
func SetDefault(id string) error {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if id != "" {
		found := false
		for _, p := range allOf(s) {
			if p.ID == id && !p.Disabled {
				found = true
			}
		}
		if !found {
			return errNoPreset(id)
		}
	}
	s.Default = id
	cache = nil
	return write(s)
}

// Put adds or replaces a preset: one that isn't a built-in goes to Custom
// (making a new one, or replacing the user's), and one that is keeps its
// identity in Builtin as an edit of the shipped preset.
func Put(p Preset) error {
	if err := p.Valid(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if shipped := shippedOf(p.ID); shipped != nil {
		p.Builtin = true
		if s.Builtin == nil {
			s.Builtin = map[string]Preset{}
		}
		s.Builtin[p.ID] = p
		cache = nil
		return write(s)
	}
	p.Builtin = false
	for i, c := range s.Custom {
		if c.ID == p.ID {
			s.Custom[i] = p
			cache = nil
			return write(s)
		}
	}
	s.Custom = append(s.Custom, p)
	cache = nil
	return write(s)
}

// Reset puts a built-in back as it ships. A user's preset can't be reset.
func Reset(id string) error {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if shippedOf(id) == nil {
		return errNoPreset(id)
	}
	delete(s.Builtin, id)
	cache = nil
	return write(s)
}

// Remove removes a user's preset. A built-in stays (Reset its edit
// instead).
func Remove(id string) error {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	if shippedOf(id) != nil {
		return errBuiltin(id)
	}
	out := s.Custom[:0]
	found := false
	for _, c := range s.Custom {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return errNoPreset(id)
	}
	s.Custom = out
	// nothing points at a preset that is gone
	if s.Default == id {
		s.Default = ""
	}
	for k, v := range s.Agents {
		if v == id {
			delete(s.Agents, k)
		}
	}
	cache = nil
	return write(s)
}

// Copy copies a preset under a new id, the user's: the way a user starts
// from a built-in.
func Copy(id, toID, name string) (Preset, error) {
	src, ok := Get(id)
	if !ok {
		return Preset{}, errNoPreset(id)
	}
	if toID == "" {
		toID = id + "-copy"
	}
	for n := 2; ; n++ {
		if _, exists := Get(toID); !exists {
			break
		}
		toID = id + "-copy-" + itoa(n)
	}
	p := src.Clone()
	p.ID, p.Name, p.Builtin, p.Version = toID, name, false, ""
	if p.Name == "" {
		p.Name = toID
	}
	if err := Put(p); err != nil {
		return Preset{}, err
	}
	return p, nil
}

// write is Save with the lock already held, writing back what load read.
func write(s Store) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	cache = &s
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type notFound string

func (e notFound) Error() string { return "no preset called " + string(e) }

func errNoPreset(id string) error { return notFound(id) }

type builtinErr string

func (e builtinErr) Error() string {
	return string(e) + " is one of magpie's own presets: edit it, or copy it, rather than removing it"
}

func errBuiltin(id string) error { return builtinErr(id) }
