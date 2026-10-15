//go:build darwin

package main

import "github.com/egoist/mygo/ui"

// MyGo caches the system font catalog and handles native resource ownership.
func enumerateNativeFontFamilies() ([]string, error) {
	families := ui.FontFamilies()
	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.Name)
	}
	return names, nil
}
