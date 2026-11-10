//go:build !windows && !darwin && !linux

package secretstore

type unavailableStore struct{}

func Open(string) Store { return unavailableStore{} }

func (unavailableStore) Read() ([]byte, error) { return nil, ErrUnavailable }
func (unavailableStore) Write([]byte) error    { return ErrUnavailable }
func (unavailableStore) Delete() error         { return ErrUnavailable }
