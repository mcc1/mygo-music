package secretstore

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

type linuxStore string

func Open(dataDir string) Store { return linuxStore(key(dataDir)) }

type secretAPI struct {
	hash, equal  uintptr
	table        func(uintptr, uintptr) uintptr
	insert       func(uintptr, uintptr, uintptr) int32
	tableFree    func(uintptr)
	duplicate    func(string) uintptr
	free         func(uintptr)
	errorFree    func(uintptr)
	cancellable  func() uintptr
	cancel       func(uintptr)
	unref        func(uintptr)
	lookup       func(uintptr, uintptr, uintptr, *uintptr) unsafe.Pointer
	store        func(uintptr, uintptr, uintptr, string, string, uintptr, *uintptr) int32
	clear        func(uintptr, uintptr, uintptr, *uintptr) int32
	passwordFree func(unsafe.Pointer)
	strlen       func(unsafe.Pointer) uintptr
}

var openSecretService = sync.OnceValues(func() (secretAPI, error) {
	var a secretAPI
	lib, err := purego.Dlopen("libsecret-1.so.0", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return a, ErrUnavailable
	}
	a.hash, err = purego.Dlsym(lib, "g_str_hash")
	if err != nil {
		return a, ErrUnavailable
	}
	a.equal, err = purego.Dlsym(lib, "g_str_equal")
	if err != nil {
		return a, ErrUnavailable
	}
	purego.RegisterLibFunc(&a.table, lib, "g_hash_table_new")
	purego.RegisterLibFunc(&a.insert, lib, "g_hash_table_insert")
	purego.RegisterLibFunc(&a.tableFree, lib, "g_hash_table_unref")
	purego.RegisterLibFunc(&a.duplicate, lib, "g_strdup")
	purego.RegisterLibFunc(&a.free, lib, "g_free")
	purego.RegisterLibFunc(&a.errorFree, lib, "g_error_free")
	purego.RegisterLibFunc(&a.cancellable, lib, "g_cancellable_new")
	purego.RegisterLibFunc(&a.cancel, lib, "g_cancellable_cancel")
	purego.RegisterLibFunc(&a.unref, lib, "g_object_unref")
	purego.RegisterLibFunc(&a.lookup, lib, "secret_password_lookupv_sync")
	purego.RegisterLibFunc(&a.store, lib, "secret_password_storev_sync")
	purego.RegisterLibFunc(&a.clear, lib, "secret_password_clearv_sync")
	purego.RegisterLibFunc(&a.passwordFree, lib, "secret_password_free")
	purego.RegisterLibFunc(&a.strlen, lib, "strlen")
	return a, nil
})

func (s linuxStore) withSecret(run func(secretAPI, uintptr, uintptr, *uintptr) error) error {
	a, err := openSecretService()
	if err != nil {
		return err
	}
	name, value := a.duplicate("mygo-music-youtube-session"), a.duplicate(string(s))
	defer a.free(name)
	defer a.free(value)
	attrs := a.table(a.hash, a.equal)
	defer a.tableFree(attrs)
	a.insert(attrs, name, value)
	cancel := a.cancellable()
	defer a.unref(cancel)
	done := make(chan struct{})
	timeout := time.AfterFunc(15*time.Second, func() {
		a.cancel(cancel)
		close(done)
	})
	defer func() {
		if !timeout.Stop() {
			<-done
		}
	}()
	var failure uintptr
	err = run(a, attrs, cancel, &failure)
	if failure != 0 {
		a.errorFree(failure)
		return fmt.Errorf("Secret Service operation failed: %w", ErrUnavailable)
	}
	return err
}

func (s linuxStore) Read() ([]byte, error) {
	var result []byte
	err := s.withSecret(func(a secretAPI, attrs, cancel uintptr, failure *uintptr) error {
		if value := a.lookup(0, attrs, cancel, failure); value != nil {
			defer a.passwordFree(value)
			result = slices.Clone(unsafe.Slice((*byte)(value), int(a.strlen(value))))
		}
		return nil
	})
	return result, err
}

func (s linuxStore) Write(data []byte) error {
	return s.withSecret(func(a secretAPI, attrs, cancel uintptr, failure *uintptr) error {
		if a.store(0, attrs, 0, service, string(data), cancel, failure) == 0 {
			return errors.New("Secret Service did not store the session")
		}
		return nil
	})
}

func (s linuxStore) Delete() error {
	return s.withSecret(func(a secretAPI, attrs, cancel uintptr, failure *uintptr) error {
		if a.clear(0, attrs, cancel, failure) == 0 {
			// libsecret does not delete locked items. Let the host record a
			// local sign-out marker when removal cannot be confirmed.
			return errors.New("Secret Service did not remove an unlocked session")
		}
		return nil
	})
}
