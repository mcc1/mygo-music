package secretstore

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

type keychainStore string

func Open(dataDir string) Store { return keychainStore(key(dataDir)) }

type keychainAPI struct {
	find    func(uintptr, uint32, string, uint32, string, *uint32, *unsafe.Pointer, *uintptr) int32
	add     func(uintptr, uint32, string, uint32, string, uint32, *byte, *uintptr) int32
	modify  func(uintptr, uintptr, uint32, *byte) int32
	remove  func(uintptr) int32
	free    func(uintptr, unsafe.Pointer) int32
	release func(uintptr)
}

var openKeychain = sync.OnceValues(func() (keychainAPI, error) {
	var api keychainAPI
	security, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return api, ErrUnavailable
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return api, ErrUnavailable
	}
	purego.RegisterLibFunc(&api.find, security, "SecKeychainFindGenericPassword")
	purego.RegisterLibFunc(&api.add, security, "SecKeychainAddGenericPassword")
	purego.RegisterLibFunc(&api.modify, security, "SecKeychainItemModifyAttributesAndData")
	purego.RegisterLibFunc(&api.remove, security, "SecKeychainItemDelete")
	purego.RegisterLibFunc(&api.free, security, "SecKeychainItemFreeContent")
	purego.RegisterLibFunc(&api.release, cf, "CFRelease")
	return api, nil
})

const (
	keychainNotFound  = -25300
	keychainDuplicate = -25299
)

func (s keychainStore) Read() ([]byte, error) {
	api, err := openKeychain()
	if err != nil {
		return nil, err
	}
	var n uint32
	var data unsafe.Pointer
	status := api.find(0, uint32(len(service)), service, uint32(len(s)), string(s), &n, &data, nil)
	if status == keychainNotFound {
		return nil, nil
	}
	if status != 0 {
		return nil, keychainError(status)
	}
	defer api.free(0, data)
	return slices.Clone(unsafe.Slice((*byte)(data), int(n))), nil
}

func (s keychainStore) Write(data []byte) error {
	api, err := openKeychain()
	if err != nil {
		return err
	}
	if len(data) == 0 || uint64(len(data)) > 1<<32-1 {
		return errors.New("invalid credential size")
	}
	status := api.add(0, uint32(len(service)), service, uint32(len(s)), string(s), uint32(len(data)), &data[0], nil)
	if status == keychainDuplicate {
		var item uintptr
		status = api.find(0, uint32(len(service)), service, uint32(len(s)), string(s), nil, nil, &item)
		if status == 0 {
			defer api.release(item)
			status = api.modify(item, 0, uint32(len(data)), &data[0])
		}
	}
	return keychainError(status)
}

func (s keychainStore) Delete() error {
	api, err := openKeychain()
	if err != nil {
		return err
	}
	var item uintptr
	status := api.find(0, uint32(len(service)), service, uint32(len(s)), string(s), nil, nil, &item)
	if status == keychainNotFound {
		return nil
	}
	if status == 0 {
		defer api.release(item)
		status = api.remove(item)
	}
	return keychainError(status)
}

func keychainError(status int32) error {
	if status == 0 {
		return nil
	}
	return fmt.Errorf("Keychain operation failed (%d)", status)
}
