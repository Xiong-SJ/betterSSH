// Package keyring wraps the operating system credential store. Passwords are
// never written to disk by betterSSH itself.
package keyring

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// Service is the keyring service name used for every saved password.
const Service = "betterSSH"

// ErrNotFound is returned when no password is stored for a key.
var ErrNotFound = keyring.ErrNotFound

// Store is the small subset of keyring operations used by betterSSH.
type Store interface {
	Set(key, password string) error
	Get(key string) (string, error)
	Delete(key string) error
}

// System uses the platform keyring via zalando/go-keyring.
type System struct{}

func (System) Set(key, password string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("keyring key must not be empty")
	}
	if password == "" {
		return errors.New("password must not be empty")
	}
	if err := keyring.Set(Service, key, password); err != nil {
		return fmt.Errorf("save password in %s keyring: %w", Service, err)
	}
	return nil
}

func (System) Get(key string) (string, error) {
	password, err := keyring.Get(Service, key)
	if err != nil {
		return "", err
	}
	return password, nil
}

func (System) Delete(key string) error {
	err := keyring.Delete(Service, key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete password from %s keyring: %w", Service, err)
	}
	return nil
}

// Key returns the stable keyring key for a resolved SSH target. The key never
// contains a password.
func Key(user, hostname string, port int) string {
	if port <= 0 {
		port = 22
	}
	return fmt.Sprintf("%s@%s:%d", strings.TrimSpace(user), strings.TrimSpace(hostname), port)
}
