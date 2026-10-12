//go:build !darwin

package main

import "testing"

func TestNativeFontNonDarwinFallback(t *testing.T) {
	families, err := enumerateNativeFontFamilies()
	if err == nil || len(families) != 0 {
		t.Fatalf("unexpected enumeration: %v, %v", families, err)
	}
	catalog := makeNativeFontCatalog(families, err)
	if !catalog.fallback || catalog.label("Georgia") != "Georgia（未验证安装）" {
		t.Fatal("fallback claimed installed fonts")
	}
	selected := "Saved Font"
	tt := fontTester(&selected, &catalog)
	clickFont(t, tt, "文章字体")
	if !tt.HasText("Saved Font（未验证安装）") {
		t.Fatal("saved fallback family disappeared")
	}
	clickFont(t, tt, "Georgia（未验证安装）")
	if selected != "Georgia" {
		t.Fatalf("stored label instead of family: %q", selected)
	}
}
