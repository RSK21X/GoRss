package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Reading surfaces share one warm neutral family; content has the lightest face.
var (
	nativeSidebarSurface  = ui.Hex("#f0eee8")
	nativeListSurface     = ui.Hex("#f7f5f0")
	nativePaperSurface    = ui.Hex("#fdfcf9")
	nativeSelectedSurface = ui.Hex("#e9e3d8")
	nativeSummarySurface  = ui.Hex("#f4f1e9")
	nativeFilledStar      = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path fill="currentColor" d="M12 2.8 14.84 8.55 21.2 9.48 16.6 13.96 17.69 20.3 12 17.31 6.31 20.3 7.4 13.96 2.8 9.48 9.16 8.55Z"/></svg>`))
)

const (
	nativeUIBodySize      float32 = 13
	nativeSectionSize     float32 = 17
	nativeListTitleSize   float32 = 15
	nativeCaptionSize     float32 = 11
	nativeControlDuration         = 140 * time.Millisecond
)

func nativeControlMotion(button ui.Element) ui.Element {
	scale := float32(1)
	if button.Pressed() && !button.IsDisabled() {
		scale = .95
	}
	return button.Transition(ui.ElementTransition{Duration: nativeControlDuration, Colors: true}).AnimatePose(ui.Pose{Scale: scale}, ui.Animation{Duration: 100 * time.Millisecond})
}

// TabsBase provides native tab roles, focus traversal, arrows, Home and End.
// Only the thumb moves; labels retain their layout and hit targets.
func nativeSegmented(c *ui.Context, selected *int, label string, labels ...string) ui.TabsParts {
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Label(label).MinWidth(0).Grow(1).MaxWidth(272).Height(34).Padding(3).Gap(2).Radius(10).Background(c.Theme().Text.Alpha(.055)).Font("").FontSize(12)
	segments := make([]ui.Element, len(labels))
	tabs.List.Children(func() {
		for i, name := range labels {
			tab := tabs.Tab(i).Label(name).Width(0).Grow(1).Shrink(1).MinWidth(0).Height(28).Padding(4, 8).Radius(7).Justify(ui.Center).AlignItems(ui.Center)
			segments[i] = tab
			tab.Children(func() { components.Text(c, name).SingleLine() })
		}
	})
	// Input is applied while each Tab is constructed. Read the final selection
	// after all tabs exist, including when arrows move to an earlier sibling.
	var active ui.Element
	for i, tab := range segments {
		color, weight := c.Theme().TextMuted, 500
		if i == *selected {
			active = tab
			color, weight = c.Theme().Text, 600
		}
		tab.TextColor(color).FontWeight(weight)
	}
	tabs.List.Draw(func(p *ui.Painter, r ui.Rect) {
		target := active.LayoutBounds()
		x, w := target.X-r.X, target.W
		if !c.Preferences().ReduceMotion {
			x = tabs.List.Animate("thumb-x", x, nativeControlDuration)
			w = tabs.List.Animate("thumb-width", w, nativeControlDuration)
		}
		thumb := ui.Rect{X: r.X + x, Y: r.Y + 3, W: w, H: 28}
		p.Fill(thumb, nativePaperSurface, 7)
		p.Stroke(thumb, c.Theme().Border.Alpha(.6), 7, 1)
	})
	return tabs
}

type nativeCopyFeedback struct {
	text  string
	until time.Time
}

func (s nativeCopyFeedback) active(now time.Time, text string) bool {
	return text != "" && s.text == text && now.Before(s.until)
}
func nativeCopyButton(c *ui.Context, label, text string, disabled, iconOnly bool, state *nativeCopyFeedback) ui.Element {
	copied := state.active(c.Now(), text)
	icon := lucide.Copy()
	if copied {
		icon = lucide.Check()
		c.After(state.until.Sub(c.Now()))
	}
	b := components.Button(c, "", components.ButtonGhost).Label(label).Font("").FontSize(nativeUIBodySize).AlignItems(ui.Center).Gap(6).Disabled(disabled)
	if iconOnly {
		b.Size(30, 30).Padding(6).Shrink(0)
	} else {
		b.Padding(7, 10)
	}
	b.Children(func() {
		color := c.Theme().TextMuted
		if copied {
			color = c.Theme().Success
		}
		components.Icon(c, icon).Size(16, 16).TextColor(color)
		if !iconOnly {
			caption := label
			if copied {
				caption = "已复制"
			}
			components.Text(c, caption).SingleLine()
		}
	})
	hint := label
	if copied {
		hint = "已复制"
	}
	components.Tooltip(c, b, hint)
	nativeControlMotion(b)
	b.OnClick(func() {
		c.WriteClipboard(text)
		*state = nativeCopyFeedback{text: text, until: c.Now().Add(1800 * time.Millisecond)}
		c.Announce("已复制")
	})
	return b
}
