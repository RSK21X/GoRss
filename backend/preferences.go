package backend

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

type Preferences struct {
	FontFamily     string  `json:"fontFamily"`
	FontSize       float64 `json:"fontSize"`
	LineHeight     float64 `json:"lineHeight"`
	ContentWidth   float64 `json:"contentWidth"`
	ShowImages     bool    `json:"showImages"`
	Theme          string  `json:"theme"`
	BilingualStyle string  `json:"bilingualStyle"`
}

func DefaultPreferences() Preferences {
	return Preferences{FontSize: 18, LineHeight: 1.8, ContentWidth: 760, ShowImages: true, Theme: "light", BilingualStyle: "soft"}
}

func validatePreferences(p Preferences) error {
	if len(p.FontFamily) > 200 || strings.ContainsAny(p.FontFamily, "\x00\r\n") {
		return errors.New("字体名称无效")
	}
	for _, v := range []float64{p.FontSize, p.LineHeight, p.ContentWidth} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("排版数值无效")
		}
	}
	if p.FontSize < 12 || p.FontSize > 32 || p.LineHeight < 1.2 || p.LineHeight > 2.4 || p.ContentWidth < 420 || p.ContentWidth > 1100 {
		return errors.New("排版数值超出允许范围")
	}
	if p.Theme != "light" {
		return errors.New("当前仅支持浅色主题")
	}
	if p.BilingualStyle != "" && p.BilingualStyle != "soft" && p.BilingualStyle != "line" && p.BilingualStyle != "plain" {
		return errors.New("双语译文样式无效")
	}
	return nil
}

// Older releases stored the reading style in ai.json. Read only that field,
// without changing the AI file. Explicit reading preferences take precedence.
func legacyBilingualStyle(dir string) string {
	f, err := os.Open(filepath.Join(dir, "ai.json"))
	if err != nil {
		return "soft"
	}
	defer f.Close()
	var legacy struct {
		Style string `json:"bilingual_style"`
	}
	if json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&legacy) == nil {
		switch legacy.Style {
		case "soft", "line", "plain":
			return legacy.Style
		}
	}
	return "soft"
}

func (r *Reader) Preferences() (Preferences, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(filepath.Dir(r.storePath), "preferences.json")
	defaults := DefaultPreferences()
	defaults.BilingualStyle = legacyBilingualStyle(filepath.Dir(r.storePath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return DefaultPreferences(), err
	}
	p := &defaults
	if err = json.Unmarshal(data, &p); err != nil {
		return DefaultPreferences(), err
	}
	if p == nil {
		return DefaultPreferences(), errors.New("preferences must be a JSON object")
	}
	if err = validatePreferences(*p); err != nil {
		return DefaultPreferences(), err
	}
	return *p, nil
}

func (r *Reader) SavePreferences(p Preferences) (Preferences, error) {
	p.FontFamily = strings.TrimSpace(p.FontFamily)
	if err := validatePreferences(p); err != nil {
		return Preferences{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := filepath.Dir(r.storePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Preferences{}, err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Preferences{}, err
	}
	f, err := os.CreateTemp(dir, ".preferences-*.json")
	if err != nil {
		return Preferences{}, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, "preferences.json"))
	}
	if err != nil {
		return Preferences{}, err
	}
	return p, nil
}
