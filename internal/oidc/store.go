package oidc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Store keeps one session per environment, readable only by the operator.
type Store struct {
	Dir string
}

// StateDir returns where sessions live.
func StateDir(getenv func(string) string) (string, error) {
	if dir := getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "fleet"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the session directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "fleet"), nil
}

func (s Store) path(environment string) string {
	return filepath.Join(s.Dir, environment+".json")
}

// Load returns the environment's session, if there is one.
func (s Store) Load(environment string) (Session, bool, error) {
	data, err := os.ReadFile(s.path(environment))
	if errors.Is(err, fs.ErrNotExist) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("read the session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, false, fmt.Errorf("the session for %q is unreadable: %w", environment, err)
	}
	return session, true, nil
}

// Save replaces the environment's session.
func (s Store) Save(environment string, session Session) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode the session: %w", err)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create the session directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.Dir, environment+".*.tmp")
	if err != nil {
		return fmt.Errorf("write the session: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write the session: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write the session: %w", err)
	}
	if err := os.Rename(temporary.Name(), s.path(environment)); err != nil {
		return fmt.Errorf("write the session: %w", err)
	}
	return nil
}

// Delete forgets the environment's session.
func (s Store) Delete(environment string) error {
	err := os.Remove(s.path(environment))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete the session: %w", err)
	}
	return nil
}
