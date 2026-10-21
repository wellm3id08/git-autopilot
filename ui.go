package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

const (
	emptyText       = "Nothing new to upload.\nDrag files here to add them to this project."
	prefLastProject = "lastProject"
	prefLastAccount = "lastAccount"
	newTokenURL     = "https://github.com/settings/tokens/new?scopes=repo&description=Git%20Autopilot"
)

type ui struct {
	app fyne.App
	win fyne.Window

	accounts []Account
	current  *Account
	proj     *Project
	changes  []Change
	checked  map[string]bool
	busy     bool

	projectViewToggle func(show bool)

	accountSelect *widget.Select
	addAccountBtn *widget.Button
	projectTitle  *widget.Label
	projectLink   *widget.Hyperlink
	chooseBtn     *widget.Button
	dropHint      *fyne.Container
	newRepoBox    *fyne.Container
	goneBox       *fyne.Container
	goneBtn       *widget.Button
	moreBtn       *widget.Button
	pending       bool
	repoName      *widget.Entry
	private       *widget.Check
	changesHeader *widget.Label
	toggleAllBtn  *widget.Button
	changesList   *widget.List
	emptyLabel    *widget.Label
	title         *widget.Entry
	description   *widget.Entry
	uploadBtn     *widget.Button
	progress      *widget.ProgressBarInfinite
	status        *widget.Label
}

func newUI(a fyne.App) *ui {
	u := &ui{app: a, win: a.NewWindow("Git Autopilot"), checked: map[string]bool{}}
	u.build()
	u.win.SetOnDropped(u.onDrop)
	a.Lifecycle().SetOnEnteredForeground(func() { u.refresh() })
	u.win.Resize(fyne.NewSize(560, 720))

	if last := a.Preferences().String(prefLastProject); last != "" {
		if _, err := os.Stat(last); err == nil {
			u.loadFolder(last)
		}
	}
	u.loadAccounts()
	return u
}

func (u *ui) build() {
	u.accountSelect = widget.NewSelect(nil, u.onAccountPicked)
	u.accountSelect.PlaceHolder = "Looking for your GitHub account…"
	u.addAccountBtn = widget.NewButtonWithIcon("Add account", theme.ContentAddIcon(), u.showSignIn)
	accountRow := container.NewBorder(nil, nil, widget.NewLabel("You are"), u.addAccountBtn, u.accountSelect)

	u.projectTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.projectLink = widget.NewHyperlink("View on GitHub", nil)
	u.projectLink.Hide()
	u.chooseBtn = widget.NewButtonWithIcon("Choose folder…", theme.FolderOpenIcon(), u.chooseFolder)

	dropText := widget.NewLabelWithStyle("Drag your project folder into this window", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	u.dropHint = container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(container.NewGridWrap(fyne.NewSize(72, 72), widget.NewIcon(theme.FolderIcon()))),
		dropText,
		container.NewCenter(widget.NewButtonWithIcon("Or choose it here", theme.FolderOpenIcon(), u.chooseFolder)),
		layout.NewSpacer(),
	)

	u.repoName = widget.NewEntry()
	u.repoName.SetPlaceHolder("Project name, or a link to an existing GitHub project")
	u.private = widget.NewCheck("Keep it private (only you can see it)", nil)
	u.private.SetChecked(true)
	u.newRepoBox = container.NewVBox(
		widget.NewLabel("This folder isn't on GitHub yet. It will be created as:"),
		u.repoName,
		u.private,
	)
	u.newRepoBox.Hide()

	goneText := widget.NewLabel("This project is no longer on GitHub. It was deleted, or this account can't see it.")
	goneText.Wrapping = fyne.TextWrapWord
	u.goneBtn = widget.NewButtonWithIcon("Upload it as a new project", theme.UploadIcon(), u.disconnect)
	u.goneBox = container.NewVBox(goneText, u.goneBtn)
	u.goneBox.Hide()

	u.moreBtn = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), nil)
	u.moreBtn.OnTapped = u.showMoreMenu

	u.changesHeader = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.toggleAllBtn = widget.NewButton("Untick all", u.toggleAll)
	u.emptyLabel = widget.NewLabelWithStyle(emptyText, fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	u.changesList = widget.NewList(
		func() int { return len(u.changes) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, widget.NewCheck("", nil), widget.NewLabel("Changed"), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := u.changes[i]
			row := o.(*fyne.Container)
			name := row.Objects[0].(*widget.Label)
			chk := row.Objects[1].(*widget.Check)
			kind := row.Objects[2].(*widget.Label)
			name.SetText(c.Path)
			name.Truncation = fyne.TextTruncateEllipsis
			kind.SetText(c.Kind)
			chk.OnChanged = nil
			chk.SetChecked(u.checked[c.Path])
			chk.OnChanged = func(v bool) {
				u.checked[c.Path] = v
				u.updateHeader()
			}
		},
	)
	changesArea := container.NewStack(u.changesList, container.NewCenter(u.emptyLabel))

	u.title = widget.NewEntry()
	u.title.SetPlaceHolder("What did you change? e.g. Added the new logo")
	u.description = widget.NewMultiLineEntry()
	u.description.SetPlaceHolder("More details (optional)")
	u.description.SetMinRowsVisible(3)
	u.uploadBtn = widget.NewButtonWithIcon("Upload to GitHub", theme.UploadIcon(), u.onUpload)
	u.uploadBtn.Importance = widget.HighImportance
	u.progress = widget.NewProgressBarInfinite()
	u.progress.Hide()
	u.status = widget.NewLabel("")
	u.status.Wrapping = fyne.TextWrapWord

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), u.refresh)
	projectRow := container.NewBorder(nil, nil, widget.NewIcon(theme.FolderIcon()), container.NewHBox(u.projectLink, u.chooseBtn, u.moreBtn), u.projectTitle)

	projectView := container.NewBorder(
		container.NewVBox(projectRow, u.goneBox, u.newRepoBox, widget.NewSeparator(),
			container.NewBorder(nil, nil, nil, container.NewHBox(u.toggleAllBtn, refreshBtn), u.changesHeader)),
		container.NewVBox(widget.NewSeparator(), u.title, u.description, u.uploadBtn, u.progress, u.status),
		nil, nil,
		changesArea,
	)
	body := container.NewStack(u.dropHint, projectView)
	projectView.Hide()
	u.win.SetContent(container.NewPadded(container.NewBorder(
		container.NewVBox(accountRow, widget.NewSeparator()), nil, nil, nil, body)))
	u.dropHint.Show()

	u.projectViewToggle = func(show bool) {
		if show {
			u.dropHint.Hide()
			projectView.Show()
		} else {
			projectView.Hide()
			u.dropHint.Show()
		}
	}
}

func (u *ui) setStatus(s string) { u.status.SetText(s) }

func (u *ui) setBusy(b bool, msg string) {
	u.busy = b
	for _, w := range []fyne.Disableable{u.uploadBtn, u.chooseBtn, u.accountSelect, u.addAccountBtn, u.title, u.description, u.repoName, u.private, u.toggleAllBtn, u.moreBtn, u.goneBtn} {
		if b {
			w.Disable()
		} else {
			w.Enable()
		}
	}
	if b {
		u.progress.Show()
	} else {
		u.progress.Hide()
	}
	u.setStatus(msg)
}

func (u *ui) loadAccounts() {
	saved := loadSavedAccounts(u.app.Preferences())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		accs := detectAccounts(ctx, saved)
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
		u.accountSelect.PlaceHolder = "Not signed in — click \"Add account\""
		u.accountSelect.Refresh()
		return
	}
	pick := 0
	for i, a := range accs {
		if a.Login != "" && strings.EqualFold(a.Login, prefer) {
			pick = i
		}
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
	if u.proj != nil {
		u.updateRemoteInfo()
	}
}

func (u *ui) addAccount(a *Account) {
	a.Source = "Signed in here"
	accs := []Account{*a}
	for _, existing := range u.accounts {
		if existing.Token != "" && !strings.EqualFold(existing.Login, a.Login) {
			accs = append(accs, existing)
		}
	}
	saveAccounts(u.app.Preferences(), accs)
	u.setAccounts(accs, a.Login)
	u.setStatus("Signed in as @" + a.Login + ".")
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
	info := widget.NewLabel("Getting a sign-in code from GitHub…")
	info.Wrapping = fyne.TextWrapWord
	openBtn := widget.NewButtonWithIcon("Open GitHub", theme.ComputerIcon(), nil)
	openBtn.Importance = widget.HighImportance
	openBtn.Disable()
	useKey := widget.NewButton("Use a key instead", nil)
	content := container.NewVBox(info, code, openBtn, useKey)
	d := dialog.NewCustom("Sign in to GitHub", "Cancel", content, u.win)
	d.SetOnClosed(cancel)
	useKey.OnTapped = func() { d.Hide(); u.showTokenSignIn() }
	d.Resize(fyne.NewSize(420, 0))
	d.Show()

	go func() {
		dc, err := startDeviceLogin(ctx)
		if err != nil {
			fyne.Do(func() { info.SetText(friendlyError(err)) })
			return
		}
		fyne.Do(func() {
			code.SetText(dc.UserCode)
			info.SetText("1. Click \"Open GitHub\".\n2. Paste this code there (it's already copied).\n3. Click \"Authorize\". This window will update by itself.")
			u.app.Clipboard().SetContent(dc.UserCode)
			openBtn.OnTapped = func() {
				if link, err := url.Parse(dc.VerificationURI); err == nil {
					_ = u.app.OpenURL(link)
				}
			}
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
	steps := widget.NewLabel("1. Click \"Get a key\" and sign in to GitHub if asked.\n2. Scroll down and click \"Generate token\".\n3. Copy the key GitHub shows you and paste it below.")
	steps.Wrapping = fyne.TextWrapWord
	getKey := widget.NewButtonWithIcon("Get a key", theme.ComputerIcon(), func() {
		link, _ := url.Parse(newTokenURL)
		_ = u.app.OpenURL(link)
	})
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder("Paste your key here")
	msg := widget.NewLabel("")
	msg.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(steps, getKey, key, msg)

	var d *dialog.ConfirmDialog
	d = dialog.NewCustomConfirm("Add your GitHub account", "Add", "Cancel", content, func(ok bool) {
		token := strings.TrimSpace(key.Text)
		if !ok || token == "" {
			return
		}
		u.setBusy(true, "Checking your key with GitHub…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			acc, err := accountFromToken(ctx, token, "Signed in here")
			fyne.Do(func() {
				u.setBusy(false, "")
				if err != nil {
					u.setStatus("That key didn't work: " + friendlyError(err))
					return
				}
				u.addAccount(acc)
			})
		}()
	}, u.win)
	d.Resize(fyne.NewSize(440, 0))
	d.Show()
}

func (u *ui) chooseFolder() {
	dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
		if err == nil && dir != nil {
			u.openFolder(uriPath(dir))
		}
	}, u.win)
}

func uriPath(uri fyne.URI) string {
	return filepath.Clean(filepath.FromSlash(uri.Path()))
}

// openFolder catches the common mistake of picking a folder that only wraps the
// real project, such as the folder an unzipped download creates.
func (u *ui) openFolder(path string) {
	inner := soleSubfolder(path)
	if inner == "" {
		u.loadFolder(path)
		return
	}
	msg := widget.NewLabel(fmt.Sprintf("\"%s\" only contains the folder \"%s\".\nDo you want to use \"%s\" as your project?",
		filepath.Base(path), filepath.Base(inner), filepath.Base(inner)))
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm("Use the folder inside?", "Use \""+filepath.Base(inner)+"\"", "Keep \""+filepath.Base(path)+"\"", msg, func(useInner bool) {
		if useInner {
			u.loadFolder(inner)
		} else {
			u.loadFolder(path)
		}
	}, u.win)
	d.Resize(fyne.NewSize(440, 0))
	d.Show()
}

func (u *ui) loadFolder(path string) {
	p, err := openProject(path)
	if err != nil {
		dialog.ShowInformation("Can't use this folder", friendlyError(err), u.win)
		return
	}
	u.proj = p
	u.checked = map[string]bool{}
	u.changes = nil
	u.app.Preferences().SetString(prefLastProject, p.Root)
	u.projectTitle.SetText(p.Name())
	u.repoName.SetText(repoSlug(p.Name()))
	u.projectViewToggle(true)
	u.updateRemoteInfo()
	u.setStatus("")
	u.refresh()
}

func (u *ui) updateRemoteInfo() {
	remote := u.proj.RemoteURL()
	u.goneBox.Hide()
	if remote == "" {
		u.newRepoBox.Show()
		u.projectLink.Hide()
		return
	}
	u.newRepoBox.Hide()
	if link, err := url.Parse(webURL(remote)); err == nil && link.Host != "" {
		u.projectLink.SetURL(link)
		u.projectLink.Show()
	} else {
		u.projectLink.Hide()
	}
	if u.current == nil {
		return
	}
	p, acc := u.proj, *u.current
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if p.RemoteGone(ctx, &acc) {
			fyne.Do(func() {
				if p == u.proj && p.RemoteURL() == remote {
					u.showGone()
				}
			})
		}
	}()
}

func (u *ui) showGone() {
	u.projectLink.Hide()
	u.goneBox.Show()
}

func (u *ui) showMoreMenu() {
	if u.proj == nil {
		return
	}
	switchItem := fyne.NewMenuItem("Upload to a different GitHub project…", func() {
		dialog.ShowConfirm("Upload somewhere else?",
			"This folder will stop uploading to its current GitHub project.\nNothing is deleted, here or on GitHub. Your next upload creates a new project.",
			func(ok bool) {
				if ok {
					u.disconnect()
				}
			}, u.win)
	})
	switchItem.Disabled = u.proj.RemoteURL() == ""
	menu := fyne.NewMenu("", switchItem)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(u.moreBtn).Add(fyne.NewPos(0, u.moreBtn.Size().Height))
	widget.ShowPopUpMenuAtPosition(menu, u.win.Canvas(), pos)
}

func (u *ui) disconnect() {
	if err := u.proj.Disconnect(); err != nil {
		dialog.ShowInformation("Something went wrong", friendlyError(err), u.win)
		return
	}
	u.repoName.SetText(repoSlug(u.proj.Name()))
	u.updateRemoteInfo()
	u.refresh()
	u.setStatus("Pick a name above and click Upload to put this folder on GitHub as a new project.")
}

func (u *ui) refresh() {
	p := u.proj
	if p == nil || u.busy {
		return
	}
	go func() {
		changes, err := p.Changes()
		pending := p.Pending()
		fyne.Do(func() {
			if p != u.proj {
				return
			}
			if err != nil {
				u.setStatus(friendlyError(err))
				return
			}
			next := map[string]bool{}
			for _, c := range changes {
				v, known := u.checked[c.Path]
				next[c.Path] = v || !known
			}
			u.checked = next
			u.changes = changes
			u.pending = pending
			u.changesList.Refresh()
			u.updateHeader()
		})
	}()
}

func (u *ui) updateHeader() {
	n := 0
	for _, c := range u.changes {
		if u.checked[c.Path] {
			n++
		}
	}
	if len(u.changes) == 0 {
		u.changesHeader.SetText("Files to upload")
		if u.pending {
			u.emptyLabel.SetText("Your files are saved but not on GitHub yet.\nClick \"Upload to GitHub\" to send them.")
		} else {
			u.emptyLabel.SetText(emptyText)
		}
		u.emptyLabel.Show()
		u.toggleAllBtn.Hide()
		return
	}
	u.emptyLabel.Hide()
	u.toggleAllBtn.Show()
	u.changesHeader.SetText(fmt.Sprintf("Files to upload (%d of %d)", n, len(u.changes)))
	if n == len(u.changes) {
		u.toggleAllBtn.SetText("Untick all")
	} else {
		u.toggleAllBtn.SetText("Tick all")
	}
}

func (u *ui) toggleAll() {
	all := true
	for _, c := range u.changes {
		all = all && u.checked[c.Path]
	}
	for _, c := range u.changes {
		u.checked[c.Path] = !all
	}
	u.changesList.Refresh()
	u.updateHeader()
}

func (u *ui) onDrop(_ fyne.Position, uris []fyne.URI) {
	if u.busy {
		return
	}
	var paths []string
	for _, uri := range uris {
		if uri.Scheme() == "file" {
			paths = append(paths, uriPath(uri))
		}
	}
	if len(paths) == 0 {
		return
	}
	if u.proj == nil {
		if fi, err := os.Stat(paths[0]); err == nil && fi.IsDir() && len(paths) == 1 {
			u.openFolder(paths[0])
		} else {
			dialog.ShowInformation("Pick your project first", "Drag the folder that holds your project into this window first. After that you can drop files into it.", u.win)
		}
		return
	}

	var outside, clashes []string
	for _, p := range paths {
		if isInside(u.proj.Root, p) {
			continue
		}
		if isInside(p, u.proj.Root) {
			dialog.ShowInformation("Can't do that", "You can't put a folder inside itself.", u.win)
			return
		}
		outside = append(outside, p)
		if _, err := os.Lstat(filepath.Join(u.proj.Root, filepath.Base(p))); err == nil {
			clashes = append(clashes, filepath.Base(p))
		}
	}
	if len(outside) == 0 {
		u.refresh()
		return
	}
	if len(clashes) == 0 {
		u.copyIn(outside, false)
		return
	}
	msg := fmt.Sprintf("Your project already has %s.\nReplace with the new version, or keep both?", strings.Join(clashes, ", "))
	if len(clashes) > 3 {
		msg = fmt.Sprintf("Your project already has %d of these files.\nReplace them with the new versions, or keep both?", len(clashes))
	}
	dialog.ShowCustomConfirm("Replace files?", "Replace", "Keep both", widget.NewLabel(msg), func(replace bool) {
		u.copyIn(outside, replace)
	}, u.win)
}

func (u *ui) copyIn(paths []string, replace bool) {
	root := u.proj.Root
	u.setBusy(true, "Adding files…")
	go func() {
		var err error
		for _, p := range paths {
			if err = copyInto(p, root, replace); err != nil {
				break
			}
		}
		fyne.Do(func() {
			u.setBusy(false, "")
			if err != nil {
				u.setStatus("Couldn't add a file: " + friendlyError(err))
			} else {
				u.setStatus(fmt.Sprintf("Added %d item(s) to your project.", len(paths)))
			}
			u.refresh()
		})
	}()
}

var nonSlug = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func repoSlug(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.TrimSpace(name), "-"), "-.")
	if s == "" {
		return "my-project"
	}
	return s
}

func autoTitle(paths []string) string {
	if len(paths) == 0 {
		return "Update project"
	}
	names := make([]string, 0, 3)
	for _, p := range paths[:min(3, len(paths))] {
		names = append(names, filepath.Base(p))
	}
	t := "Update " + strings.Join(names, ", ")
	if len(paths) > 3 {
		t += fmt.Sprintf(" and %d more", len(paths)-3)
	}
	return t
}

func (u *ui) onUpload() {
	if u.proj == nil || u.busy {
		return
	}
	if u.current == nil || u.current.Token == "" {
		u.setStatus("Sign in to GitHub first, then click Upload again.")
		u.showSignIn()
		return
	}
	var paths []string
	for _, c := range u.changes {
		if u.checked[c.Path] {
			paths = append(paths, c.Path)
		}
	}
	title := strings.TrimSpace(u.title.Text)
	if title == "" {
		title = autoTitle(paths)
	}
	desc := u.description.Text
	acc := *u.current
	p := u.proj
	repoInput := strings.TrimSpace(u.repoName.Text)
	private := u.private.Checked

	u.setBusy(true, "Getting ready…")
	step := func(s string) { fyne.Do(func() { u.setStatus(s) }) }
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		created, err := u.ensureRemote(ctx, p, &acc, repoInput, private, step)
		var up *UploadResult
		if err == nil {
			up, err = p.Upload(ctx, &acc, paths, title, desc, step)
		}
		fyne.Do(func() {
			u.setBusy(false, "")
			u.updateRemoteInfo()
			defer u.refresh()
			if err != nil {
				u.setStatus("")
				dialog.ShowInformation("Upload didn't finish", friendlyError(err), u.win)
				if errors.Is(err, transport.ErrRepositoryNotFound) {
					u.showGone()
				}
				return
			}
			u.showResult(p, up, created)
		})
	}()
}

func (u *ui) ensureRemote(ctx context.Context, p *Project, acc *Account, input string, private bool, step func(string)) (bool, error) {
	if p.RemoteURL() != "" {
		return false, nil
	}
	if i := strings.Index(input, "github.com"); i >= 0 {
		path := strings.Trim(strings.TrimSuffix(strings.TrimSpace(input[i+len("github.com"):]), ".git"), "/:")
		if strings.Count(path, "/") != 1 {
			return false, fmt.Errorf("that GitHub link doesn't look right; it should look like https://github.com/name/project")
		}
		return false, p.SetRemote("https://github.com/" + path + ".git")
	}
	step("Creating your project on GitHub…")
	repo, err := createOrFindRepo(ctx, acc, repoSlug(input), private)
	if err != nil {
		return false, err
	}
	return true, p.SetRemote(repo.CloneURL)
}

func (u *ui) showResult(p *Project, up *UploadResult, created bool) {
	link, _ := url.Parse(webURL(p.RemoteURL()))
	switch {
	case up.SideBranch != "":
		if link != nil {
			link = link.JoinPath("tree", up.SideBranch)
		}
		u.resultDialog("Your work is safe",
			"Someone changed the same files on GitHub since your last upload, so your version was saved next to theirs as \""+up.SideBranch+"\" instead of replacing it.\n\nOpen GitHub to compare the two and pick what to keep.", link)
	case up.NothingToDo && !up.Merged:
		u.setStatus("Everything is already on GitHub. Nothing new to upload.")
	default:
		u.title.SetText("")
		u.description.SetText("")
		msg := "Your changes are on GitHub."
		if created {
			msg = "Your project is now on GitHub."
		}
		if up.Merged {
			msg += "\nChanges made on GitHub were also downloaded into your folder."
		}
		u.setStatus("Uploaded " + time.Now().Format("15:04") + ".")
		u.resultDialog("Done!", msg, link)
	}
}

func (u *ui) resultDialog(title, msg string, link *url.URL) {
	label := widget.NewLabel(msg)
	label.Wrapping = fyne.TextWrapWord
	if link == nil {
		dialog.ShowCustom(title, "OK", label, u.win)
		return
	}
	dialog.ShowCustomConfirm(title, "Open on GitHub", "Close", label, func(open bool) {
		if open {
			_ = u.app.OpenURL(link)
		}
	}, u.win)
}
