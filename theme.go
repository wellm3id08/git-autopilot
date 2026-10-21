package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// monoTheme swaps Fyne's blue accent for neutral greys that match the icon.
type monoTheme struct{ fyne.Theme }

func (t monoTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	dark := v == theme.VariantDark
	pick := func(d, l color.NRGBA) color.Color {
		if dark {
			return d
		}
		return l
	}
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return pick(color.NRGBA{0xE6, 0xE6, 0xE8, 0xFF}, color.NRGBA{0x1C, 0x1C, 0x1F, 0xFF})
	case theme.ColorNameForegroundOnPrimary:
		return pick(color.NRGBA{0x12, 0x12, 0x14, 0xFF}, color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF})
	case theme.ColorNameFocus:
		return pick(color.NRGBA{0xFF, 0xFF, 0xFF, 0x40}, color.NRGBA{0x00, 0x00, 0x00, 0x30})
	case theme.ColorNameSelection:
		return pick(color.NRGBA{0xFF, 0xFF, 0xFF, 0x30}, color.NRGBA{0x00, 0x00, 0x00, 0x20})
	}
	return t.Theme.Color(name, v)
}
