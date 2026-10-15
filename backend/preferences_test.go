package backend

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.json")
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Preferences()
	if err != nil || p != DefaultPreferences() {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p.FontFamily = "Songti SC"
	p.FontSize = 22
	p.LineHeight = 2
	p.ContentWidth = 900
	p.ShowImages = false
	if _, err = r.SavePreferences(p); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Preferences()
	if err != nil || got != p {
		t.Fatalf("persist: %+v %v", got, err)
	}
	for _, invalid := range []Preferences{{FontSize: math.NaN()}, {FontFamily: "bad\nfont", FontSize: 18, LineHeight: 1.8, ContentWidth: 760, Theme: "light"}} {
		if _, err = r.SavePreferences(invalid); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	got, _ = r.Preferences()
	if got != p {
		t.Fatal("invalid save changed preferences")
	}
}

func TestPreferencesRejectNullDocument(t *testing.T) {
	r := newTestReader(t)
	path := filepath.Join(filepath.Dir(r.storePath), "preferences.json")
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := r.Preferences()
	if err == nil || p != DefaultPreferences() {
		t.Fatalf("null preferences accepted: %+v %v", p, err)
	}
}

func TestPreferencesSaveFailurePreservesSavedValues(t *testing.T) {
	r := newTestReader(t)
	p := DefaultPreferences()
	p.FontSize = 24
	if _, err := r.SavePreferences(p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(r.storePath), "preferences.json")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	p.FontSize = 26
	if _, err := r.SavePreferences(p); err == nil {
		t.Fatal("save failure ignored")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(saved) {
		t.Fatal("saved preferences changed", err)
	}
	p, err = r.Preferences()
	if err != nil || p.FontSize != 24 {
		t.Fatalf("previous preferences lost: %+v %v", p, err)
	}
}

func TestBilingualStyleMigrationAndReadingPreferencePrecedence(t *testing.T) {
	for _, style := range []string{"soft", "line", "plain"} {
		t.Run(style, func(t *testing.T) {
			r := newTestReader(t)
			dir := filepath.Dir(r.storePath)
			legacy := []byte(`{"bilingual_style":"` + style + `","custom":{"model":"retained"}}`)
			if err := os.WriteFile(filepath.Join(dir, "ai.json"), legacy, 0600); err != nil {
				t.Fatal(err)
			}
			for _, prefsJSON := range []string{"", `{"fontSize":22}`} {
				if prefsJSON != "" {
					if err := os.WriteFile(filepath.Join(dir, "preferences.json"), []byte(prefsJSON), 0600); err != nil {
						t.Fatal(err)
					}
				}
				prefs, err := r.Preferences()
				if err != nil || prefs.BilingualStyle != style {
					t.Fatalf("migration: %+v %v", prefs, err)
				}
			}
			prefs := DefaultPreferences()
			prefs.BilingualStyle = "plain"
			if _, err := r.SavePreferences(prefs); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ai.json"), []byte(`{"bilingual_style":"line"}`), 0600); err != nil {
				t.Fatal(err)
			}
			reloaded, err := r.Preferences()
			if err != nil || reloaded.BilingualStyle != "plain" || reloaded.FontSize != prefs.FontSize {
				t.Fatalf("reading preference did not override legacy: %+v %v", reloaded, err)
			}
			before, _ := os.ReadFile(filepath.Join(dir, "ai.json"))
			reloaded.BilingualStyle = "invalid"
			if _, err := r.SavePreferences(reloaded); err == nil {
				t.Fatal("invalid style saved")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "ai.json"))
			saved, err := r.Preferences()
			if string(before) != string(after) || err != nil || saved.BilingualStyle != "plain" {
				t.Fatal("style save changed AI config or lost reading preference")
			}
		})
	}
}

func TestBilingualStyleMigrationIgnoresInvalidLegacyConfig(t *testing.T) {
	for _, input := range []string{`null`, `{broken`, `{"bilingual_style":"unknown"}`, `{"custom":{"model":"retained"}}`} {
		r := newTestReader(t)
		if err := os.WriteFile(filepath.Join(filepath.Dir(r.storePath), "ai.json"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		prefs, err := r.Preferences()
		if err != nil || prefs.BilingualStyle != "soft" {
			t.Fatalf("invalid legacy changed defaults: %+v %v", prefs, err)
		}
	}
}
