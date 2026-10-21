package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	prefLastAccount = "lastAccount"
	newTokenURL     = "https://github.com/settings/tokens/new?scopes=repo&description=Git%20Autopilot"
)

type ui struct {
	app fyne.App
	win fyne.Window

	accounts []Account
	current  *Account

	accountSelect *widget.Select
	accountMenu   *widget.Button
	home          *homeView
	page          *projectPage
}

func newUI(a fyne.App) *ui {
	u := &ui{app: a, win: a.NewWindow("Git Autopilot")}

	u.accountSelect = widget.NewSelect(nil, u.onAccountPicked)
	u.accountSelect.PlaceHolder = T("Looking for your GitHub account…")
	u.accountMenu = widget.NewButtonWithIcon("", theme.AccountIcon(), u.showAccountMenu)
	header := container.NewBorder(nil, nil, widget.NewLabel(T("Account")), u.accountMenu, u.accountSelect)

	u.home = newHomeView(u)
	u.page = newProjectPage(u)
	u.page.root.Hide()

	u.win.SetContent(container.NewPadded(container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil,
		container.NewStack(u.home.root, u.page.root))))
	u.win.SetOnDropped(u.onDrop)
	a.Lifecycle().SetOnEnteredForeground(func() {
		if u.page.root.Visible() {
			u.page.refresh()
		}
	})
	u.win.Resize(fyne.NewSize(620, 780))

	u.home.reload()
	u.loadAccounts()
	return u
}

func (u *ui) signedIn() bool { return u.current != nil && u.current.Token != "" }

func (u *ui) showHome() {
	u.page.root.Hide()
	u.page.proj = nil
	u.home.root.Show()
	u.home.reload()
}

// openFolder catches the common mistake of picking a folder that only wraps the
// real project, such as the folder an unzipped download creates.
func (u *ui) openFolder(path string) {
	inner := soleSubfolder(path)
	if inner == "" {
		u.loadFolder(path)
		return
	}
	msg := widget.NewLabel(Tf("\"%s\" only contains the folder \"%s\".\nDo you want to use \"%s\" as your project?",
		filepath.Base(path), filepath.Base(inner), filepath.Base(inner)))
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm(T("Use the folder inside?"), Tf("Use \"%s\"", filepath.Base(inner)), Tf("Keep \"%s\"", filepath.Base(path)), msg, func(useInner bool) {
		if useInner {
			u.loadFolder(inner)
		} else {
			u.loadFolder(path)
		}
	}, u.win)
	d.Resize(fyne.NewSize(460, 0))
	d.Show()
}

func (u *ui) loadFolder(path string) {
	p, err := openProject(path)
	if err != nil {
		dialog.ShowInformation(T("Can't use this folder"), friendlyError(err), u.win)
		return
	}
	addToLibrary(u.app.Preferences(), p.Root)
	u.home.root.Hide()
	u.page.load(p)
	u.page.root.Show()
}

func (u *ui) chooseFolder(onPick func(string)) {
	d := dialog.NewFolderOpen(func(dir fyne.ListableURI, err error) {
		if err == nil && dir != nil {
			onPick(uriPath(dir))
		}
	}, u.win)
	d.Resize(fyne.NewSize(600, 500))
	d.Show()
}

func uriPath(uri fyne.URI) string {
	return filepath.Clean(filepath.FromSlash(uri.Path()))
}

func (u *ui) openInFileManager(path string) {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	_ = u.app.OpenURL(&url.URL{Scheme: "file", Path: p})
}

func (u *ui) openLink(link string) {
	if l, err := url.Parse(link); err == nil {
		_ = u.app.OpenURL(l)
	}
}

func (u *ui) onDrop(_ fyne.Position, uris []fyne.URI) {
	var paths []string
	for _, uri := range uris {
		if uri.Scheme() == "file" {
			paths = append(paths, uriPath(uri))
		}
	}
	if len(paths) == 0 {
		return
	}
	if u.page.root.Visible() {
		u.page.onDrop(paths)
		return
	}
	if fi, err := os.Stat(paths[0]); err == nil && fi.IsDir() && len(paths) == 1 {
		u.openFolder(paths[0])
		return
	}
	dialog.ShowInformation(T("Open a project first"), T("Drop a folder here to open it as a project. To add files to a project, open the project first and then drop the files."), u.win)
}

func (u *ui) loadAccounts() {
	saved := loadSavedAccounts(u.app.Preferences())
	hidden := hiddenAccounts(u.app.Preferences())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var accs []Account
		for _, a := range detectAccounts(ctx, saved) {
			if !hidden[strings.ToLower(a.Login)] || a.Source == "Signed in here" {
				accs = append(accs, a)
			}
		}
		fyne.Do(func() { u.setAccounts(accs, u.app.Preferences().String(prefLastAccount)) })
	}()
}

func (u *ui) setAccounts(accs []Account, prefer string) {
	u.accounts = accs
	labels := make([]string, len(accs))
	for i := range accs {
		labels[i] = accs[i].Label()
	}
	u.accountSelect.SetOptions(labels)
	if len(accs) == 0 {
		u.current = nil
		u.accountSelect.ClearSelected()
		u.accountSelect.PlaceHolder = T("Not signed in")
		u.accountSelect.Refresh()
		u.accountsChanged()
		return
	}
	pick := 0
	for i, a := range accs {
		if a.Login != "" && strings.EqualFold(a.Login, prefer) {
			pick = i
		}
	}
	if u.accountSelect.SelectedIndex() == pick {
		u.onAccountPicked("")
		return
	}
	u.accountSelect.SetSelectedIndex(pick)
}

func (u *ui) onAccountPicked(string) {
	i := u.accountSelect.SelectedIndex()
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.current = &u.accounts[i]
	if u.current.Login != "" {
		u.app.Preferences().SetString(prefLastAccount, u.current.Login)
	}
	u.accountsChanged()
}

func (u *ui) accountsChanged() {
	if u.page.root.Visible() {
		u.page.updateRemoteInfo()
	} else {
		u.home.reload()
	}
}

func (u *ui) showAccountMenu() {
	items := []*fyne.MenuItem{fyne.NewMenuItem(T("Add a GitHub account…"), u.showSignIn)}
	if u.signedIn() {
		acc := *u.current
		items = append(items, fyne.NewMenuItem(Tf("Sign out of @%s", acc.Login), func() {
			confirmDanger(u.win, T("Sign out?"), Tf("Git Autopilot will forget @%s on this computer. Your projects and files stay where they are.", acc.Login), T("Sign out"), func() {
				u.signOut(acc.Login)
			})
		}))
	}
	showMenuBelow(u.win, u.accountMenu, items...)
}

func (u *ui) signOut(login string) {
	setAccountHidden(u.app.Preferences(), login, true)
	var keep []Account
	for _, a := range u.accounts {
		if !strings.EqualFold(a.Login, login) {
			keep = append(keep, a)
		}
	}
	saveAccounts(u.app.Preferences(), keep)
	u.setAccounts(keep, "")
}

func (u *ui) addAccount(a *Account) {
	a.Source = "Signed in here"
	setAccountHidden(u.app.Preferences(), a.Login, false)
	accs := []Account{*a}
	for _, existing := range u.accounts {
		if existing.Token != "" && !strings.EqualFold(existing.Login, a.Login) {
			accs = append(accs, existing)
		}
	}
	saveAccounts(u.app.Preferences(), accs)
	u.setAccounts(accs, a.Login)
}

func (u *ui) showSignIn() {
	if githubClientID != "" {
		u.showDeviceSignIn()
		return
	}
	u.showTokenSignIn()
}

func (u *ui) showDeviceSignIn() {
	ctx, cancel := context.WithCancel(context.Background())
	code := widget.NewLabelWithStyle("…", fyne.TextAlignCenter, fyne.TextStyle{Bold: true, Monospace: true})
	code.SizeName = theme.SizeNameHeadingText
	info := widget.NewLabel(T("Getting a sign-in code from GitHub…"))
	info.Wrapping = fyne.TextWrapWord
	openBtn := widget.NewButtonWithIcon(T("Open GitHub"), theme.ComputerIcon(), nil)
	openBtn.Importance = widget.HighImportance
	openBtn.Disable()
	useKey := widget.NewButton(T("Use a key instead"), nil)
	content := container.NewVBox(info, code, openBtn, useKey)
	d := dialog.NewCustom(T("Sign in to GitHub"), T("Cancel"), content, u.win)
	d.SetOnClosed(cancel)
	useKey.OnTapped = func() { d.Hide(); u.showTokenSignIn() }
	d.Resize(fyne.NewSize(440, 0))
	d.Show()

	go func() {
		dc, err := startDeviceLogin(ctx)
		if err != nil {
			fyne.Do(func() { info.SetText(friendlyError(err)) })
			return
		}
		fyne.Do(func() {
			code.SetText(dc.UserCode)
			info.SetText(T("1. Click \"Open GitHub\".\n2. Paste this code there (it's already copied).\n3. Click \"Authorize\". This window will update by itself."))
			u.app.Clipboard().SetContent(dc.UserCode)
			openBtn.OnTapped = func() { u.openLink(dc.VerificationURI) }
			openBtn.Enable()
		})
		token, err := waitDeviceLogin(ctx, dc)
		if ctx.Err() != nil {
			return
		}
		var acc *Account
		if err == nil {
			acc, err = accountFromToken(ctx, token, "Signed in here")
		}
		fyne.Do(func() {
			if err != nil {
				info.SetText(friendlyError(err))
				return
			}
			d.Hide()
			u.addAccount(acc)
		})
	}()
}

func (u *ui) showTokenSignIn() {
	steps := widget.NewLabel(T("Git Autopilot needs a key from GitHub to upload for you. It takes a minute:\n\n1. Click \"Get a key\" and sign in to GitHub if asked.\n2. Scroll to the bottom of the page and click \"Generate token\".\n3. Copy the long code GitHub shows you and paste it below."))
	steps.Wrapping = fyne.TextWrapWord
	getKey := widget.NewButtonWithIcon(T("Get a key"), theme.ComputerIcon(), func() { u.openLink(newTokenURL) })
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder(T("Paste your key here"))
	content := container.NewVBox(steps, getKey, key)

	d := dialog.NewCustomConfirm(T("Sign in to GitHub"), T("Sign in"), T("Cancel"), content, func(ok bool) {
		token := strings.TrimSpace(key.Text)
		if !ok || token == "" {
			return
		}
		prog := dialog.NewCustomWithoutButtons(T("Checking your key with GitHub…"), widget.NewProgressBarInfinite(), u.win)
		prog.Show()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			acc, err := accountFromToken(ctx, token, "Signed in here")
			fyne.Do(func() {
				prog.Hide()
				if err != nil {
					dialog.ShowInformation(T("That key didn't work"), friendlyError(err), u.win)
					return
				}
				u.addAccount(acc)
			})
		}()
	}, u.win)
	d.Resize(fyne.NewSize(480, 0))
	d.Show()
}

func (u *ui) resultDialog(title, msg, link string) {
	label := widget.NewLabel(msg)
	label.Wrapping = fyne.TextWrapWord
	var d dialog.Dialog
	if link == "" {
		d = dialog.NewCustom(title, T("OK"), label, u.win)
	} else {
		d = dialog.NewCustomConfirm(title, T("Open on GitHub"), T("Close"), label, func(open bool) {
			if open {
				u.openLink(link)
			}
		}, u.win)
	}
	d.Resize(fyne.NewSize(440, 0))
	d.Show()
}

func confirmDanger(win fyne.Window, title, msg, action string, onYes func()) {
	label := widget.NewLabel(msg)
	label.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm(title, action, T("Cancel"), label, func(ok bool) {
		if ok {
			onYes()
		}
	}, win)
	d.Resize(fyne.NewSize(420, 0))
	d.Show()
}

func showMenuBelow(win fyne.Window, anchor fyne.CanvasObject, items ...*fyne.MenuItem) {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor).Add(fyne.NewPos(0, anchor.Size().Height))
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), win.Canvas(), pos)
}
