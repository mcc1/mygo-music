// Package secretstore keeps a plugin session in the current user's OS store.
package secretstore

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

const service = "MyGo Music YouTube session"

var ErrUnavailable = errors.New("system credential store is unavailable")

type Store interface {
	// Read returns an empty value if there is no stored session.
	Read() ([]byte, error)
	Write([]byte) error
	Delete() error
}

func key(dataDir string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(dataDir)))
}
