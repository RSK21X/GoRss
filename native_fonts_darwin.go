//go:build darwin

package main

import (
	"bytes"
	"fmt"

	"github.com/ebitengine/purego"
)

// CoreText exposes installed families; MyGo's font APIs expose shaping only.
// No Objective-C, subprocesses, or cgo are needed. Copy follows CF ownership:
// the array owns its borrowed strings, and is released after conversion.
func enumerateNativeFontFamilies() (families []string, err error) {
	// RegisterFunc can panic on an unsupported ABI. Surface that as a fallback.
	defer func() {
		if recovered := recover(); recovered != nil {
			families, err = nil, fmt.Errorf("CoreText font enumeration: %v", recovered)
		}
	}()
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	defer purego.Dlclose(cf)
	ct, err := purego.Dlopen("/System/Library/Frameworks/CoreText.framework/CoreText", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	defer purego.Dlclose(ct)

	var copyFamilies func() uintptr
	var arrayCount func(uintptr) int64
	var arrayValue func(uintptr, int64) uintptr
	var stringLength func(uintptr) int64
	var stringMaxSize func(int64, uint32) int64
	var stringCString func(uintptr, *byte, int64, uint32) bool
	var release func(uintptr)
	for _, binding := range []struct {
		handle uintptr
		name   string
		target any
	}{
		{ct, "CTFontManagerCopyAvailableFontFamilyNames", &copyFamilies},
		{cf, "CFArrayGetCount", &arrayCount},
		{cf, "CFArrayGetValueAtIndex", &arrayValue},
		{cf, "CFStringGetLength", &stringLength},
		{cf, "CFStringGetMaximumSizeForEncoding", &stringMaxSize},
		{cf, "CFStringGetCString", &stringCString},
		{cf, "CFRelease", &release},
	} {
		symbol, lookupErr := purego.Dlsym(binding.handle, binding.name)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if symbol == 0 {
			return nil, fmt.Errorf("missing CoreText symbol %s", binding.name)
		}
		purego.RegisterFunc(binding.target, symbol)
	}
	array := copyFamilies()
	if array == 0 {
		return nil, fmt.Errorf("CoreText returned no font array")
	}
	defer release(array)
	count := arrayCount(array)
	if count <= 0 || count > 100000 {
		return nil, fmt.Errorf("invalid CoreText font count: %d", count)
	}
	families = make([]string, 0, int(count))
	const utf8Encoding uint32 = 0x08000100
	for i := int64(0); i < count; i++ {
		value := arrayValue(array, i)
		if value == 0 {
			return nil, fmt.Errorf("CoreText returned null family")
		}
		size := stringMaxSize(stringLength(value), utf8Encoding)
		if size < 0 || size > 1<<20 {
			return nil, fmt.Errorf("invalid CoreText font name size: %d", size)
		}
		buffer := make([]byte, int(size)+1)
		if !stringCString(value, &buffer[0], int64(len(buffer)), utf8Encoding) {
			return nil, fmt.Errorf("cannot convert CoreText font name to UTF-8")
		}
		end := bytes.IndexByte(buffer, 0)
		if end < 0 {
			return nil, fmt.Errorf("unterminated CoreText font name")
		}
		families = append(families, string(buffer[:end]))
	}
	return families, nil
}
