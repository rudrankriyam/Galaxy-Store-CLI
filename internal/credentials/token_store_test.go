package credentials

import (
	"errors"
	"testing"

	"github.com/99designs/keyring"
)

func TestKeyringTokenStoreRoundTrip(t *testing.T) {
	ring := keyring.NewArrayKeyring(nil)
	store := newKeyringTokenStore(ring)

	if err := store.Set("production", " access-token "); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	token, err := store.Get("production")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if token != "access-token" {
		t.Fatalf("Get() = %q, want access-token", token)
	}

	item, err := ring.Get(keyringItemPrefix + "production")
	if err != nil {
		t.Fatalf("underlying Get() error = %v", err)
	}
	if string(item.Data) != "access-token" {
		t.Fatalf("stored data = %q, want access-token", item.Data)
	}

	if err := store.Delete("production"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get("production"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Get() after Delete error = %v, want ErrTokenNotFound", err)
	}
}

func TestKeyringTokenStoreValidatesInputs(t *testing.T) {
	store := newKeyringTokenStore(keyring.NewArrayKeyring(nil))

	if err := store.Set("", "token"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Set(empty profile) error = %v, want ErrIncomplete", err)
	}
	if err := store.Set("production", ""); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Set(empty token) error = %v, want ErrIncomplete", err)
	}
	if _, err := store.Get("../production"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Get(invalid profile) error = %v, want ErrIncomplete", err)
	}
}
