//go:build darwin

package aiservice

import (
	"github.com/ebitengine/purego"
	"runtime"
	"sync"
	"unsafe"
)

// KeychainStore uses Security.framework directly; no credential is placed in
// process arguments, shell input, configuration, logs, or temporary files.
type KeychainStore struct{}

func NewKeychainStore() *KeychainStore { return &KeychainStore{} }

var kc struct {
	sync.Mutex
	once    sync.Once
	err     error
	find    func(uintptr, uint32, unsafe.Pointer, uint32, unsafe.Pointer, *uint32, *unsafe.Pointer, *uintptr) int32
	add     func(uintptr, uint32, unsafe.Pointer, uint32, unsafe.Pointer, uint32, unsafe.Pointer, *uintptr) int32
	modify  func(uintptr, uintptr, uint32, unsafe.Pointer) int32
	delete  func(uintptr) int32
	free    func(uintptr, unsafe.Pointer) int32
	release func(uintptr)
}

func initKeychain() {
	kc.once.Do(func() {
		defer func() {
			if recover() != nil {
				kc.err = ErrSecretStore
			}
		}()
		lib, e := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if e != nil {
			kc.err = ErrSecretStore
			return
		}
		cf, e := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if e != nil {
			kc.err = ErrSecretStore
			return
		}
		purego.RegisterLibFunc(&kc.find, lib, "SecKeychainFindGenericPassword")
		purego.RegisterLibFunc(&kc.add, lib, "SecKeychainAddGenericPassword")
		purego.RegisterLibFunc(&kc.modify, lib, "SecKeychainItemModifyAttributesAndData")
		purego.RegisterLibFunc(&kc.delete, lib, "SecKeychainItemDelete")
		purego.RegisterLibFunc(&kc.free, lib, "SecKeychainItemFreeContent")
		purego.RegisterLibFunc(&kc.release, cf, "CFRelease")
	})
}
func kcFind(p Provider) (uint32, unsafe.Pointer, uintptr, int32) {
	service := []byte("gorss.aiservice")
	account := []byte(p)
	var size uint32
	var data unsafe.Pointer
	var item uintptr
	status := kc.find(0, uint32(len(service)), unsafe.Pointer(&service[0]), uint32(len(account)), unsafe.Pointer(&account[0]), &size, &data, &item)
	runtime.KeepAlive(service)
	runtime.KeepAlive(account)
	return size, data, item, status
}
func (*KeychainStore) Get(p Provider) (string, error) {
	if !validProvider(p) {
		return "", ErrConfig
	}
	kc.Lock()
	defer kc.Unlock()
	initKeychain()
	if kc.err != nil {
		return "", ErrSecretStore
	}
	size, data, item, status := kcFind(p)
	if item != 0 {
		defer kc.release(item)
	}
	if status == -25300 {
		return "", ErrSecretNotFound
	}
	if status != 0 {
		return "", ErrSecretStore
	}
	defer kc.free(0, data)
	if size > 16384 {
		return "", ErrSecretStore
	}
	if size == 0 {
		return "", nil
	}
	return string(unsafe.Slice((*byte)(data), int(size))), nil
}
func (*KeychainStore) Set(p Provider, key string) error {
	if !validProvider(p) || len(key) > 16384 {
		return ErrConfig
	}
	kc.Lock()
	defer kc.Unlock()
	initKeychain()
	if kc.err != nil {
		return ErrSecretStore
	}
	_, data, item, status := kcFind(p)
	if status == 0 {
		kc.free(0, data)
		defer kc.release(item)
	} else if status != -25300 {
		return ErrSecretStore
	}
	buf := append([]byte(key), 0)
	var result int32
	if status == 0 {
		result = kc.modify(item, 0, uint32(len(key)), unsafe.Pointer(&buf[0]))
	} else {
		service := []byte("gorss.aiservice")
		account := []byte(p)
		result = kc.add(0, uint32(len(service)), unsafe.Pointer(&service[0]), uint32(len(account)), unsafe.Pointer(&account[0]), uint32(len(key)), unsafe.Pointer(&buf[0]), nil)
		runtime.KeepAlive(service)
		runtime.KeepAlive(account)
	}
	runtime.KeepAlive(buf)
	clear(buf)
	if result != 0 {
		return ErrSecretStore
	}
	return nil
}
func (*KeychainStore) Delete(p Provider) error {
	if !validProvider(p) {
		return ErrConfig
	}
	kc.Lock()
	defer kc.Unlock()
	initKeychain()
	if kc.err != nil {
		return ErrSecretStore
	}
	_, data, item, status := kcFind(p)
	if status == -25300 {
		return nil
	}
	if status != 0 {
		return ErrSecretStore
	}
	kc.free(0, data)
	defer kc.release(item)
	if kc.delete(item) != 0 {
		return ErrSecretStore
	}
	return nil
}
