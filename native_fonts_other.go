//go:build !darwin

package main

import "errors"

func enumerateNativeFontFamilies() ([]string, error) {
	return nil, errors.New("installed font enumeration requires macOS CoreText")
}
