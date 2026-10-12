package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/backend"
)

func TestNativeFontCatalog(t *testing.T) {
	catalog := makeNativeFontCatalog([]string{" 中文字体 ", "Georgia", "Arial", "Georgia", "", "bad\nname"}, nil)
	if catalog.fallback || !reflect.DeepEqual(catalog.families, []string{"Arial", "Georgia", "中文字体"}) {
		t.Fatalf("catalog: %+v", catalog)
	}
	if catalog.label("") != "系统默认" || catalog.label("Georgia") != "Georgia" || catalog.label("Missing") != "Missing（未安装）" {
		t.Fatal("font availability labels are incorrect")
	}
	for _, input := range []struct {
		names []string
		err   error
	}{{nil, nil}, {[]string{""}, nil}, {[]string{"Partial"}, errors.New("failed")}} {
		fallback := makeNativeFontCatalog(input.names, input.err)
		if !fallback.fallback || len(fallback.families) == 0 || !strings.Contains(fallback.description(), "未验证安装") || !strings.Contains(fallback.label("Georgia"), "未验证安装") {
			t.Fatalf("unlabeled fallback: %+v", fallback)
		}
	}
}

func TestNativeFontCacheConcurrent(t *testing.T) {
	var cache nativeFontCache
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			catalog := cache.get(func() ([]string, error) {
				calls.Add(1)
				return []string{"Georgia", "Arial"}, nil
			})
			if catalog.label("Georgia") != "Georgia" || len(catalog.families) != 2 {
				t.Error("incomplete published catalog")
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("enumeration ran %d times", calls.Load())
	}
	cache.get(func() ([]string, error) { t.Fatal("enumerated again"); return nil, nil })
}

func fontTester(selected *string, catalog *nativeFontCatalog) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		theme.Apply(c, theme.Light)
		ui.Column(c).Padding(20).Children(func() { nativeFontDropdown(c, selected, catalog) })
	}, 600, 500)
}

func clickFont(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFontDropdownSearchAndSelection(t *testing.T) {
	catalog := makeNativeFontCatalog([]string{"Arial", "Georgia", "Menlo", "中文字体"}, nil)
	selected := "Missing saved family"
	tt := fontTester(&selected, &catalog)
	clickFont(t, tt, "文章字体")
	if !tt.HasText("Missing saved family（未安装）") || !tt.HasText("系统默认") {
		t.Fatalf("options: %v", tt.Texts())
	}
	clickFont(t, tt, "搜索字体")
	tt.Type("gEo")
	if !tt.HasText("Georgia") || tt.HasText("Arial") {
		t.Fatalf("filter: %v", tt.Texts())
	}
	clickFont(t, tt, "Georgia")
	if selected != "Georgia" {
		t.Fatalf("selection: %q", selected)
	}
	clickFont(t, tt, "文章字体")
	if !tt.HasText("Arial") {
		t.Fatal("query not reset on reopen")
	}
	clickFont(t, tt, "搜索字体")
	tt.Type("不存在")
	if !tt.HasText("没有匹配的字体") || selected != "Georgia" {
		t.Fatal("search changed selection or lost empty state")
	}
	tt.Key(0, ui.KeyEscape)
	clickFont(t, tt, "文章字体")
	clickFont(t, tt, "系统默认")
	if selected != "" {
		t.Fatalf("system default stored %q", selected)
	}
	clickFont(t, tt, "文章字体")
	clickFont(t, tt, "中文字体")
	if selected != "中文字体" {
		t.Fatal("Unicode selection failed")
	}
}

func TestNativeFontDropdownDraftPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.json")
	r, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(r)
	defer a.close()
	// A zero window invalidates safely without creating a native window.
	a.window = &mygo.Window{}
	catalog := makeNativeFontCatalog([]string{"Georgia", "Menlo"}, nil)
	tt := fontTester(&a.draft.FontFamily, &catalog)
	clickFont(t, tt, "文章字体")
	clickFont(t, tt, "Georgia")
	if a.draft.FontFamily != "Georgia" || a.prefs.FontFamily != "" {
		t.Fatal("selection did not remain a draft")
	}
	before, err := r.Preferences()
	if err != nil || before.FontFamily != "" {
		t.Fatalf("persisted before save: %+v %v", before, err)
	}
	a.saveSettings()
	job := <-a.jobs
	if err := job.work(a.ctx); err != nil {
		t.Fatal(err)
	}
	// A newer dropdown edit must survive completion of the earlier save.
	clickFont(t, tt, "文章字体")
	clickFont(t, tt, "Menlo")
	a.pending--
	job.done(nil)
	if a.prefs.FontFamily != "Georgia" || a.draft.FontFamily != "Menlo" {
		t.Fatalf("save overwrote draft: %+v %+v", a.prefs, a.draft)
	}
	a.saveSettings()
	job = <-a.jobs
	if err := job.work(a.ctx); err != nil {
		t.Fatal(err)
	}
	a.pending--
	job.done(nil)
	if a.prefs != a.draft {
		t.Fatal("completed save left draft dirty")
	}
	reopened, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.Preferences()
	if err != nil || persisted.FontFamily != "Menlo" {
		t.Fatalf("reopened: %+v %v", persisted, err)
	}
}
