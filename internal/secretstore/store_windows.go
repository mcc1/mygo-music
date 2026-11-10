package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dpapiStore string

func Open(dataDir string) Store {
	return dpapiStore(filepath.Join(dataDir, "youtube-session.dpapi"))
}

func (s dpapiStore) Read() ([]byte, error) {
	data, err := os.ReadFile(string(s))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dpapi(data, false)
}

func (s dpapiStore) Write(data []byte) error {
	sealed, err := dpapi(data, true)
	if err != nil {
		return err
	}
	path := string(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".youtube-session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(sealed)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s dpapiStore) Delete() error {
	if err := os.Remove(string(s)); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func dpapi(data []byte, protect bool) ([]byte, error) {
	if len(data) == 0 || uint64(len(data)) > 1<<32-1 {
		return nil, errors.New("invalid credential size")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return slices.Clone(unsafe.Slice(out.Data, int(out.Size))), nil
}
