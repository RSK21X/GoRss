package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// Published once and thereafter read-only, including its slices and map.
// Font installation changes become visible on the next application launch.
type nativeFontCatalog struct {
	families  []string
	available map[string]bool
	fallback  bool
}

type nativeFontCache struct {
	once    sync.Once
	catalog nativeFontCatalog
}

var installedNativeFonts nativeFontCache

func (cache *nativeFontCache) get(enumerate func() ([]string, error)) *nativeFontCatalog {
	cache.once.Do(func() {
		families, err := enumerate()
		cache.catalog = makeNativeFontCatalog(families, err)
	})
	return &cache.catalog
}

func makeNativeFontCatalog(families []string, err error) nativeFontCatalog {
	clean := func(names []string) []string {
		seen := make(map[string]bool)
		out := make([]string, 0, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" && !strings.ContainsAny(name, "\x00\r\n") && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			li, lj := strings.ToLower(out[i]), strings.ToLower(out[j])
			if li == lj {
				return out[i] < out[j]
			}
			return li < lj
		})
		return out
	}
	catalog := nativeFontCatalog{families: clean(families)}
	if err != nil || len(catalog.families) == 0 {
		catalog.fallback = true
		catalog.families = clean([]string{"Arial", "Georgia", "Helvetica", "Menlo", "PingFang SC", "Songti SC", "Times New Roman"})
	}
	catalog.available = make(map[string]bool, len(catalog.families))
	for _, family := range catalog.families {
		catalog.available[family] = true
	}
	return catalog
}

func (catalog *nativeFontCatalog) label(family string) string {
	if family == "" {
		return "系统默认"
	}
	if catalog.fallback {
		return family + "（未验证安装）"
	}
	if !catalog.available[family] {
		return family + "（未安装）"
	}
	return family
}

func (catalog *nativeFontCatalog) description() string {
	if catalog.fallback {
		return "无法读取本机字体列表，显示常见字体（未验证安装）。可搜索选择；系统默认始终可用。"
	}
	return fmt.Sprintf("从本机 %d 个已安装字体家族中搜索选择；系统默认使用系统字体。", len(catalog.families))
}

func nativeFontDropdown(c *ui.Context, selected *string, catalog *nativeFontCatalog) {
	sel := components.Select(c, selected)
	sel.Trigger.Label("文章字体").Width(260).Shrink(0).Children(func() {
		components.Text(c, catalog.label(*selected)).SingleLine()
	})
	query := ui.Local(sel.Trigger, "fontSearch", func() string { return "" })
	if !sel.Open() {
		*query = ""
	}
	sel.Popup(func(panel ui.Element) {
		panel.Width(320).Children(func() {
			sel.SearchField(query).Label("搜索字体").Placeholder("搜索已安装字体")
			ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
				q := strings.ToLower(strings.TrimSpace(*query))
				count := 0
				option := func(family string) {
					label := catalog.label(family)
					if q != "" && !strings.Contains(strings.ToLower(label), q) {
						return
					}
					count++
					// Select items handle input before returning in MyGo 0.5.
					c.Key(family)
					sel.Item(family).FillWidth().Children(func() { components.Text(c, label).SingleLine() })
				}
				option("")
				// Keep saved values selectable without silently replacing them.
				if *selected != "" && !catalog.available[*selected] {
					option(*selected)
				}
				for _, family := range catalog.families {
					option(family)
				}
				if count == 0 {
					components.Text(c, "没有匹配的字体").TextColor(c.Theme().TextMuted)
				}
			})
		})
	})
}
