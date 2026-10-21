package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type homeRow struct {
	Title    string
	Subtitle string
	Path     string
	Repo     *ghRepo
	Attn     bool
}

type homeView struct {
	u    *ui
	root *fyne.Container

	rows  []homeRow
	shown []homeRow
	gen   int

	search    *widget.Entry
	list      *widget.List
	empty     *widget.Label
	signInBox *fyne.Container
	progress  *widget.ProgressBarInfinite
	status    *widget.Label
}

func newHomeView(u *ui) *homeView {
	h := &homeView{u: u}

	heading := widget.NewLabelWithStyle(T("Your projects"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	heading.SizeName = theme.SizeNameSubHeadingText
	newBtn := widget.NewButtonWithIcon(T("New project"), theme.ContentAddIcon(), h.newProject)
	newBtn.Importance = widget.HighImportance
	intro := widget.NewLabel(T("A project is a folder on your computer that is kept safe on GitHub. Open one to upload your changes, or start a new one from any folder."))
	intro.Wrapping = fyne.TextWrapWord

	h.search = widget.NewEntry()
	h.search.SetPlaceHolder(T("Search your projects"))
	h.search.OnChanged = func(string) { h.applyFilter() }

	h.list = widget.NewList(
		func() int { return len(h.shown) },
		func() fyne.CanvasObject {
			title := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			title.Truncation = fyne.TextTruncateEllipsis
			sub := widget.NewLabel("")
			sub.Truncation = fyne.TextTruncateEllipsis
			sub.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, widget.NewIcon(theme.FolderIcon()), widget.NewButton("", nil),
				container.NewVBox(title, sub))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			r := h.shown[i]
			row := o.(*fyne.Container)
			text := row.Objects[0].(*fyne.Container)
			icon := row.Objects[1].(*widget.Icon)
			btn := row.Objects[2].(*widget.Button)
			text.Objects[0].(*widget.Label).SetText(r.Title)
			sub := text.Objects[1].(*widget.Label)
			sub.SetText(r.Subtitle)
			if r.Attn {
				sub.Importance = widget.WarningImportance
			} else {
				sub.Importance = widget.LowImportance
			}
			sub.Refresh()
			if r.Path != "" {
				icon.SetResource(theme.FolderIcon())
				btn.SetText(T("Open"))
				btn.SetIcon(theme.FolderOpenIcon())
			} else {
				icon.SetResource(theme.StorageIcon())
				btn.SetText(T("Download"))
				btn.SetIcon(theme.DownloadIcon())
			}
			btn.OnTapped = func() { h.activate(r) }
		},
	)
	h.list.OnSelected = func(i widget.ListItemID) {
		h.list.UnselectAll()
		if i < len(h.shown) {
			h.activate(h.shown[i])
		}
	}

	h.empty = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	h.empty.Wrapping = fyne.TextWrapWord

	signInText := widget.NewLabelWithStyle(T("Sign in to GitHub to see your projects and upload new ones."), fyne.TextAlignCenter, fyne.TextStyle{})
	signInText.Wrapping = fyne.TextWrapWord
	signInBtn := widget.NewButtonWithIcon(T("Sign in to GitHub"), theme.LoginIcon(), u.showSignIn)
	signInBtn.Importance = widget.HighImportance
	h.signInBox = container.NewVBox(signInText, container.NewCenter(signInBtn), widget.NewSeparator())
	h.signInBox.Hide()

	h.progress = widget.NewProgressBarInfinite()
	h.progress.Hide()
	h.status = widget.NewLabel("")
	h.status.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(
		container.NewBorder(nil, nil, nil, newBtn, heading),
		intro,
		h.signInBox,
		h.search,
	)
	h.root = container.NewBorder(top, container.NewVBox(h.progress, h.status), nil, nil,
		container.NewStack(h.list, container.NewVBox(layout.NewSpacer(), h.empty, layout.NewSpacer())))
	return h
}

type localInfo struct {
	path     string
	name     string
	fullName string
	changes  int
	pending  bool
	linked   bool
}

// reload rebuilds the list from the folders on this computer and, when signed
// in, the account's GitHub projects. A generation counter drops stale results
// when the account changes while a reload is still running.
func (h *homeView) reload() {
	h.gen++
	gen := h.gen
	paths := loadLibrary(h.u.app.Preferences())
	var acc *Account
	if h.u.signedIn() {
		a := *h.u.current
		acc = &a
		h.signInBox.Hide()
	} else {
		h.signInBox.Show()
	}
	h.progress.Show()
	h.status.SetText(T("Loading your projects…"))

	go func() {
		var locals []localInfo
		for _, path := range paths {
			p, err := openProject(path)
			if err != nil {
				continue
			}
			changes, _ := p.Changes()
			locals = append(locals, localInfo{
				path:     p.Root,
				name:     p.Name(),
				fullName: p.FullName(),
				changes:  len(changes),
				pending:  p.Pending(),
				linked:   p.RemoteURL() != "",
			})
		}
		var repos []ghRepo
		var repoErr error
		if acc != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			repos, repoErr = listRepos(ctx, acc.Token)
			cancel()
		}
		fyne.Do(func() {
			if gen != h.gen {
				return
			}
			h.progress.Hide()
			h.status.SetText("")
			if repoErr != nil {
				h.status.SetText(T("Couldn't load your GitHub projects: ") + friendlyError(repoErr))
			}
			h.rows = buildRows(locals, repos)
			h.applyFilter()
		})
	}()
}

func buildRows(locals []localInfo, repos []ghRepo) []homeRow {
	var rows []homeRow
	onDisk := map[string]bool{}
	for _, l := range locals {
		r := homeRow{Title: l.name, Path: l.path}
		switch {
		case !l.linked:
			r.Subtitle = T("On this computer · not on GitHub yet")
			r.Attn = true
		case l.changes > 0:
			r.Subtitle = Tf("On this computer · %d changes to upload", l.changes)
			r.Attn = true
		case l.pending:
			r.Subtitle = T("On this computer · waiting to upload")
			r.Attn = true
		default:
			r.Subtitle = T("On this computer · everything uploaded")
		}
		if l.fullName != "" {
			onDisk[strings.ToLower(l.fullName)] = true
			if !strings.EqualFold(filepath.Base(l.fullName), l.name) {
				r.Title += "  (" + l.fullName + ")"
			}
		}
		rows = append(rows, r)
	}
	for i := range repos {
		repo := repos[i]
		if onDisk[strings.ToLower(repo.FullName)] {
			continue
		}
		vis := T("public")
		if repo.Private {
			vis = T("private")
		}
		rows = append(rows, homeRow{
			Title:    repo.Name,
			Subtitle: T("Only on GitHub") + " · " + vis + " · " + ago(repo.PushedAt),
			Repo:     &repo,
		})
	}
	return rows
}

func (h *homeView) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(h.search.Text))
	h.shown = h.shown[:0]
	for _, r := range h.rows {
		if q == "" || strings.Contains(strings.ToLower(r.Title), q) {
			h.shown = append(h.shown, r)
		}
	}
	h.list.Refresh()
	switch {
	case len(h.shown) > 0:
		h.empty.Hide()
	case q != "":
		h.empty.SetText(T("No project matches your search."))
		h.empty.Show()
	default:
		h.empty.SetText(T("No projects yet.\nClick \"New project\" or drag a folder into this window."))
		h.empty.Show()
	}
}

func (h *homeView) activate(r homeRow) {
	if r.Path != "" {
		h.u.loadFolder(r.Path)
		return
	}
	if r.Repo != nil {
		h.askDownload(r.Repo)
	}
}

func (h *homeView) newProject() {
	msg := widget.NewLabel(T("Pick the folder that holds your files. Git Autopilot will show you what's inside and put it on GitHub when you click Upload.\n\nTip: you can also drag a folder into this window."))
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm(T("New project"), T("Choose folder…"), T("Cancel"), msg, func(ok bool) {
		if ok {
			h.u.chooseFolder(h.u.openFolder)
		}
	}, h.u.win)
	d.Resize(fyne.NewSize(440, 0))
	d.Show()
}

func (h *homeView) askDownload(repo *ghRepo) {
	parent := defaultDownloadDir()
	where := widget.NewLabel("")
	where.Wrapping = fyne.TextWrapWord
	update := func() { where.SetText(Tf("It will be saved in:\n%s", freeFolder(parent, repo.Name))) }
	update()
	change := widget.NewButtonWithIcon(T("Change location…"), theme.FolderOpenIcon(), func() {
		h.u.chooseFolder(func(dir string) {
			parent = dir
			update()
		})
	})
	intro := widget.NewLabel(Tf("Download a copy of \"%s\" to this computer so you can work on it.", repo.Name))
	intro.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm(T("Download project"), T("Download"), T("Cancel"),
		container.NewVBox(intro, where, change), func(ok bool) {
			if ok {
				h.download(repo, freeFolder(parent, repo.Name))
			}
		}, h.u.win)
	d.Resize(fyne.NewSize(460, 0))
	d.Show()
}

func (h *homeView) download(repo *ghRepo, dest string) {
	var acc *Account
	if h.u.signedIn() {
		a := *h.u.current
		acc = &a
	}
	h.progress.Show()
	h.status.SetText(Tf("Downloading \"%s\"…", repo.Name))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := os.MkdirAll(filepath.Dir(dest), 0o755)
		var p *Project
		if err == nil {
			p, err = Download(ctx, acc, repo, dest)
		}
		fyne.Do(func() {
			h.progress.Hide()
			h.status.SetText("")
			if err != nil {
				dialog.ShowInformation(T("Download didn't finish"), friendlyError(err), h.u.win)
				return
			}
			h.u.loadFolder(p.Root)
		})
	}()
}

func ago(t time.Time) string {
	if t.IsZero() {
		return T("never updated")
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return T("updated just now")
	case d < 24*time.Hour:
		return Tf("updated %d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return T("updated yesterday")
	case d < 60*24*time.Hour:
		return Tf("updated %d days ago", int(d.Hours()/24))
	}
	return Tf("updated %s", t.Format("2006-01-02"))
}
