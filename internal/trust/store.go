package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Entry struct {
	RepoPath       string     `json:"repo_path"`
	DefinitionHash string     `json:"verification_definition_hash"`
	SourceType     string     `json:"source_type"`
	Commands       [][]string `json:"argv"`
}

type fileFormat struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

type Store struct {
	path string
}

func New(stateDir string) Store {
	return Store{path: filepath.Join(stateDir, "trust.json")}
}

func (s Store) List() ([]Entry, error) {
	state, err := s.read()
	if err != nil {
		return nil, err
	}
	entries := append([]Entry{}, state.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RepoPath < entries[j].RepoPath })
	return entries, nil
}

func (s Store) Revoke(repo string) (bool, error) {
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return false, fmt.Errorf("cannot resolve repository path")
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return false, fmt.Errorf("cannot resolve repository path")
	}
	state, err := s.read()
	if err != nil {
		return false, err
	}
	kept := state.Entries[:0]
	found := false
	for _, entry := range state.Entries {
		if entry.RepoPath == canonical {
			found = true
			continue
		}
		kept = append(kept, entry)
	}
	if !found {
		return false, nil
	}
	state.Entries = kept
	if err := s.write(state); err != nil {
		return false, err
	}
	return true, nil
}

func (s Store) read() (fileFormat, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileFormat{Version: 1, Entries: []Entry{}}, nil
	}
	if err != nil {
		return fileFormat{}, fmt.Errorf("cannot open trust state")
	}
	defer file.Close()
	var state fileFormat
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != 1 {
		return fileFormat{}, fmt.Errorf("invalid trust state")
	}
	return state, nil
}

func (s Store) write(state fileFormat) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("cannot create state directory")
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".trust-*.json")
	if err != nil {
		return fmt.Errorf("cannot create trust state")
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("cannot protect trust state")
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("cannot write trust state")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("cannot sync trust state")
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("cannot close trust state")
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("cannot replace trust state")
	}
	return nil
}
