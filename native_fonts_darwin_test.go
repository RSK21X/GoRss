//go:build darwin

package main

import "testing"

func TestNativeCoreTextFontEnumeration(t *testing.T) {
	families, err := enumerateNativeFontFamilies()
	if err != nil {
		t.Fatalf("CoreText enumeration failed: %v", err)
	}
	catalog := makeNativeFontCatalog(families, nil)
	if catalog.fallback || len(catalog.families) == 0 {
		t.Fatal("CoreText returned no usable installed families")
	}
	for _, family := range catalog.families {
		if catalog.label(family) != family {
			t.Fatalf("installed family mislabeled: %q", family)
		}
	}
}
