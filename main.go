package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"
)

//go:embed assets/icon.png
var iconPNG []byte

var version = "dev"

func main() {
	a := app.NewWithID("io.github.gitautopilot")
	icon := fyne.NewStaticResource("icon.png", iconPNG)
	a.SetIcon(icon)
	a.Settings().SetTheme(monoTheme{theme.DefaultTheme()})
	u := newUI(a)
	u.win.SetTitle("Git Autopilot " + version)
	u.win.SetIcon(icon)
	u.win.ShowAndRun()
}
