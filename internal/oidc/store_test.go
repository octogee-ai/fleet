package oidc

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "state", "fleet")}
}

func TestStoreRoundTripsOneSessionPerEnvironment(t *testing.T) {
	store := newStore(t)
	if err := store.Save("test", Session{IDToken: "t", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("prod", Session{IDToken: "p"}); err != nil {
		t.Fatal(err)
	}
	session, found, err := store.Load("test")
	if err != nil || !found || session.IDToken != "t" || session.RefreshToken != "r" {
		t.Fatalf("session %+v, found %v, error %v", session, found, err)
	}
}

func TestStoreKeepsSessionsPrivateToTheOperator(t *testing.T) {
	store := newStore(t)
	if err := store.Save("test", Session{IDToken: "t"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Stat(filepath.Join(store.Dir, "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Mode().Perm(); got != 0o600 {
		t.Fatalf("session file mode %o", got)
	}
	directory, err := os.Stat(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := directory.Mode().Perm(); got != 0o700 {
		t.Fatalf("session directory mode %o", got)
	}
}

func TestStoreReplacingASessionLeavesNoTemporaryFiles(t *testing.T) {
	store := newStore(t)
	for _, token := range []string{"first", "second"} {
		if err := store.Save("test", Session{IDToken: token}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one session file, saw %v", entries)
	}
	session, _, err := store.Load("test")
	if err != nil || session.IDToken != "second" {
		t.Fatalf("session %+v, error %v", session, err)
	}
}

func TestStoreReportsAMissingSession(t *testing.T) {
	_, found, err := newStore(t).Load("test")
	if err != nil || found {
		t.Fatalf("found %v, error %v", found, err)
	}
}

func TestStoreRefusesAnUnreadableSession(t *testing.T) {
	store := newStore(t)
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "test.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load("test"); err == nil {
		t.Fatal("expected a refusal")
	}
}

func TestStoreDeleteForgetsTheSession(t *testing.T) {
	store := newStore(t)
	if err := store.Save("test", Session{IDToken: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("test"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load("test"); err != nil || found {
		t.Fatalf("found %v, error %v", found, err)
	}
}

func TestStoreDeleteOfAMissingSessionSucceeds(t *testing.T) {
	if err := newStore(t).Delete("test"); err != nil {
		t.Fatal(err)
	}
}

func TestStateDirPrefersTheXDGDirectory(t *testing.T) {
	dir, err := StateDir(func(name string) string {
		if name == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	})
	if err != nil || dir != filepath.Join("/state", "fleet") {
		t.Fatalf("dir %q, error %v", dir, err)
	}
}
