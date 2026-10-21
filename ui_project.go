package main

import (
	"context"
	"errors"
	"fmt"
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

type projectPage struct {
	u    *ui
	root *fyne.Container

	proj    *Project
	changes []Change
	checked map[string]bool
	pending bool
	files   []string
	history []Version
	busy    bool

	title       *widget.Label
	githubBtn   *widget.Button
	folderBtn   *widget.Button
	moreBtn     *widget.Button
	backBtn     *widget.Button
	newRepoBox  *fyne.Container
	repoName    *widget.Entry
	private     *widget.Check
	goneBox     *fyne.Container
	goneBtn     *widget.Button
	tabs        *container.AppTabs
	changesTab  *container.TabItem
	changesHead *widget.Label
	toggleAll   *widget.Button
	changesList *widget.List
	changesNone *fyne.Container
	filesList   *widget.List
	historyList *widget.List
	message     *widget.Entry
	details     *widget.Entry
	uploadBtn   *widget.Button
	progress    *widget.ProgressBarInfinite
	status      *widget.Label
}

func newProjectPage(u *ui) *projectPage {
	pg := &projectPage{u: u, checked: map[string]bool{}}

	pg.backBtn = widget.NewButtonWithIcon(T("Projects"), theme.NavigateBackIcon(), u.showHome)
	pg.title = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	pg.title.SizeName = theme.SizeNameSubHeadingText
	pg.title.Truncation = fyne.TextTruncateEllipsis
	pg.folderBtn = widget.NewButtonWithIcon(T("Folder"), theme.FolderOpenIcon(), func() { u.openInFileManager(pg.proj.Root) })
	pg.githubBtn = widget.NewButtonWithIcon("GitHub", theme.ComputerIcon(), func() { u.openLink(webURL(pg.proj.RemoteURL())) })
	pg.moreBtn = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), pg.showMenu)
	header := container.NewBorder(nil, nil, pg.backBtn, container.NewHBox(pg.folderBtn, pg.githubBtn, pg.moreBtn), pg.title)

	pg.repoName = widget.NewEntry()
	pg.repoName.SetPlaceHolder(T("Name for the project on GitHub"))
	pg.private = widget.NewCheck(T("Private: only you can see it on GitHub"), nil)
	pg.private.SetChecked(true)
	newText := widget.NewLabel(T("This folder isn't on GitHub yet. When you click Upload, it will be created with this name:"))
	newText.Wrapping = fyne.TextWrapWord
	pg.newRepoBox = container.NewVBox(newText, pg.repoName, pg.private)

	goneText := widget.NewLabel(T("This project is no longer on GitHub. It was deleted, or this account can't see it."))
	goneText.Wrapping = fyne.TextWrapWord
	goneText.Importance = widget.WarningImportance
	pg.goneBtn = widget.NewButtonWithIcon(T("Upload it as a new project"), theme.UploadIcon(), pg.disconnect)
	pg.goneBox = container.NewVBox(goneText, pg.goneBtn)
	pg.goneBox.Hide()

	pg.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon(T("Changes"), theme.UploadIcon(), pg.buildChangesTab()),
		container.NewTabItemWithIcon(T("All files"), theme.FileIcon(), pg.buildFilesTab()),
		container.NewTabItemWithIcon(T("History"), theme.HistoryIcon(), pg.buildHistoryTab()),
	)
	pg.changesTab = pg.tabs.Items[0]

	pg.progress = widget.NewProgressBarInfinite()
	pg.progress.Hide()
	pg.status = widget.NewLabel("")
	pg.status.Wrapping = fyne.TextWrapWord

	pg.root = container.NewBorder(
		container.NewVBox(header, pg.goneBox, pg.newRepoBox, widget.NewSeparator()),
		container.NewVBox(pg.progress, pg.status),
		nil, nil,
		pg.tabs,
	)
	return pg
}

func (pg *projectPage) buildChangesTab() fyne.CanvasObject {
	pg.changesHead = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	pg.toggleAll = widget.NewButton(T("Untick all"), pg.toggleAllFiles)
	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), pg.refresh)

	pg.changesList = widget.NewList(
		func() int { return len(pg.changes) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			kind := widget.NewLabel("")
			undo := widget.NewButtonWithIcon("", theme.ContentUndoIcon(), nil)
			undo.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, widget.NewCheck("", nil), container.NewHBox(kind, undo), name)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := pg.changes[i]
			row := o.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(c.Path)
			chk := row.Objects[1].(*widget.Check)
			right := row.Objects[2].(*fyne.Container)
			right.Objects[0].(*widget.Label).SetText(T(c.Kind))
			right.Objects[1].(*widget.Button).OnTapped = func() { pg.undo(c) }
			chk.OnChanged = nil
			chk.SetChecked(pg.checked[c.Path])
			chk.OnChanged = func(v bool) {
				pg.checked[c.Path] = v
				pg.updateHeader()
			}
		},
	)

	noneText := widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	noneText.Wrapping = fyne.TextWrapWord
	openFolder := widget.NewButtonWithIcon(T("Open the project folder"), theme.FolderOpenIcon(), func() { pg.u.openInFileManager(pg.proj.Root) })
	pg.changesNone = container.NewVBox(layout.NewSpacer(), noneText, container.NewCenter(openFolder), layout.NewSpacer())

	hint := widget.NewLabel(T("Ticked files will be uploaded. Use ↶ to undo a change or remove a file you added by mistake."))
	hint.Wrapping = fyne.TextWrapWord

	pg.message = widget.NewEntry()
	pg.message.SetPlaceHolder(T("What did you change? (optional) e.g. Added the new logo"))
	pg.details = widget.NewMultiLineEntry()
	pg.details.SetPlaceHolder(T("More details (optional)"))
	pg.details.SetMinRowsVisible(2)
	pg.uploadBtn = widget.NewButtonWithIcon(T("Upload to GitHub"), theme.UploadIcon(), pg.upload)
	pg.uploadBtn.Importance = widget.HighImportance

	return container.NewBorder(
		container.NewVBox(container.NewBorder(nil, nil, nil, container.NewHBox(pg.toggleAll, refresh), pg.changesHead), hint),
		container.NewVBox(widget.NewSeparator(), pg.message, pg.details, pg.uploadBtn),
		nil, nil,
		container.NewStack(pg.changesList, pg.changesNone),
	)
}

func (pg *projectPage) noneLabel() *widget.Label {
	return pg.changesNone.Objects[1].(*widget.Label)
}

func (pg *projectPage) buildFilesTab() fyne.CanvasObject {
	hint := widget.NewLabel(T("Everything in this project. Deleting a file here also removes it from GitHub at your next upload."))
	hint.Wrapping = fyne.TextWrapWord
	pg.filesList = widget.NewList(
		func() int { return len(pg.files) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), nil)
			del.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, widget.NewIcon(theme.FileIcon()), del, name)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			path := pg.files[i]
			row := o.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(path)
			row.Objects[2].(*widget.Button).OnTapped = func() { pg.deleteFile(path) }
		},
	)
	return container.NewBorder(hint, nil, nil, nil, pg.filesList)
}

func (pg *projectPage) buildHistoryTab() fyne.CanvasObject {
	hint := widget.NewLabel(T("Every upload is saved on GitHub as a version. The newest is at the top."))
	hint.Wrapping = fyne.TextWrapWord
	pg.historyList = widget.NewList(
		func() int { return len(pg.history) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.Truncation = fyne.TextTruncateEllipsis
			sub := widget.NewLabel("")
			sub.Importance = widget.LowImportance
			return container.NewVBox(t, sub)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			v := pg.history[i]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(v.Title)
			box.Objects[1].(*widget.Label).SetText(v.When.Local().Format("2006-01-02 15:04") + " · " + v.Author)
		},
	)
	return container.NewBorder(hint, nil, nil, nil, pg.historyList)
}

func (pg *projectPage) load(p *Project) {
	pg.proj = p
	pg.checked = map[string]bool{}
	pg.changes, pg.files, pg.history = nil, nil, nil
	pg.title.SetText(p.Name())
	pg.repoName.SetText(repoSlug(p.Name()))
	pg.message.SetText("")
	pg.details.SetText("")
	pg.status.SetText("")
	pg.tabs.SelectIndex(0)
	pg.updateRemoteInfo()
	pg.refresh()
	pg.autoSync()
}

func (pg *projectPage) setBusy(b bool, msg string) {
	pg.busy = b
	for _, w := range []fyne.Disableable{pg.uploadBtn, pg.backBtn, pg.moreBtn, pg.message, pg.details, pg.repoName, pg.private, pg.toggleAll, pg.goneBtn} {
		if b {
			w.Disable()
		} else {
			w.Enable()
		}
	}
	if b {
		pg.progress.Show()
	} else {
		pg.progress.Hide()
	}
	pg.status.SetText(msg)
}

func (pg *projectPage) updateRemoteInfo() {
	p := pg.proj
	if p == nil {
		return
	}
	remote := p.RemoteURL()
	pg.goneBox.Hide()
	if remote == "" {
		pg.newRepoBox.Show()
		pg.githubBtn.Hide()
		return
	}
	pg.newRepoBox.Hide()
	if webURL(remote) != "" {
		pg.githubBtn.Show()
	} else {
		pg.githubBtn.Hide()
	}
	if !pg.u.signedIn() {
		return
	}
	acc := *pg.u.current
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if p.RemoteGone(ctx, &acc) {
			fyne.Do(func() {
				if p == pg.proj && p.RemoteURL() == remote {
					pg.showGone()
				}
			})
		}
	}()
}

func (pg *projectPage) showGone() {
	pg.githubBtn.Hide()
	pg.goneBox.Show()
}

// autoSync quietly brings in changes made on GitHub when a project is opened,
// so what the user sees in the folder matches GitHub as closely as possible.
func (pg *projectPage) autoSync() {
	p := pg.proj
	if p.RemoteURL() == "" || !pg.u.signedIn() {
		return
	}
	acc := *pg.u.current
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		changed, err := p.Sync(ctx, &acc)
		fyne.Do(func() {
			if p != pg.proj || pg.busy {
				return
			}
			switch {
			case errors.Is(err, errConflict):
				pg.status.SetText(T("GitHub has newer versions of files you are editing. Upload when you're ready and both versions will be kept."))
			case err == nil && changed:
				pg.status.SetText(T("Downloaded the latest changes from GitHub."))
				pg.refresh()
			}
		})
	}()
}

func (pg *projectPage) syncNow() {
	p := pg.proj
	if !pg.u.signedIn() {
		pg.u.showSignIn()
		return
	}
	acc := *pg.u.current
	pg.setBusy(true, T("Getting the latest version from GitHub…"))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		changed, err := p.Sync(ctx, &acc)
		fyne.Do(func() {
			pg.setBusy(false, "")
			defer pg.refresh()
			switch {
			case errors.Is(err, errConflict):
				pg.status.SetText(T("GitHub has newer versions of files you are editing. Upload when you're ready and both versions will be kept."))
			case err != nil:
				dialog.ShowInformation(T("Couldn't get the latest version"), friendlyError(err), pg.u.win)
			case changed:
				pg.status.SetText(T("Downloaded the latest changes from GitHub."))
			default:
				pg.status.SetText(T("You already have the latest version."))
			}
		})
	}()
}

func (pg *projectPage) showMenu() {
	if pg.proj == nil {
		return
	}
	latest := fyne.NewMenuItem(T("Get the latest version from GitHub"), pg.syncNow)
	latest.Disabled = pg.proj.RemoteURL() == ""
	other := fyne.NewMenuItem(T("Upload to a different GitHub project…"), func() {
		confirmDanger(pg.u.win, T("Upload somewhere else?"),
			T("This folder will stop uploading to its current GitHub project. Nothing is deleted, here or on GitHub. Your next upload creates a new project."),
			T("Continue"), pg.disconnect)
	})
	other.Disabled = pg.proj.RemoteURL() == ""
	forget := fyne.NewMenuItem(T("Remove from my project list"), func() {
		confirmDanger(pg.u.win, T("Remove from the list?"),
			T("The project disappears from Git Autopilot's list. The folder and your files stay on your computer, and nothing changes on GitHub."),
			T("Remove"), func() {
				removeFromLibrary(pg.u.app.Preferences(), pg.proj.Root)
				pg.u.showHome()
			})
	})
	showMenuBelow(pg.u.win, pg.moreBtn, latest, other, fyne.NewMenuItemSeparator(), forget)
}

func (pg *projectPage) disconnect() {
	if err := pg.proj.Disconnect(); err != nil {
		dialog.ShowInformation(T("Something went wrong"), friendlyError(err), pg.u.win)
		return
	}
	pg.repoName.SetText(repoSlug(pg.proj.Name()))
	pg.updateRemoteInfo()
	pg.refresh()
	pg.tabs.SelectIndex(0)
	pg.status.SetText(T("Choose a name above, then click Upload to put this folder on GitHub as a new project."))
}

func (pg *projectPage) refresh() {
	p := pg.proj
	if p == nil || pg.busy {
		return
	}
	go func() {
		changes, err := p.Changes()
		pending := p.Pending()
		files, _ := p.Files()
		history, _ := p.History(200)
		fyne.Do(func() {
			if p != pg.proj {
				return
			}
			if err != nil {
				pg.status.SetText(friendlyError(err))
				return
			}
			next := map[string]bool{}
			for _, c := range changes {
				v, known := pg.checked[c.Path]
				next[c.Path] = v || !known
			}
			pg.checked, pg.changes, pg.pending = next, changes, pending
			pg.files, pg.history = files, history
			pg.changesList.Refresh()
			pg.filesList.Refresh()
			pg.historyList.Refresh()
			pg.updateHeader()
		})
	}()
}

func (pg *projectPage) updateHeader() {
	n := 0
	for _, c := range pg.changes {
		if pg.checked[c.Path] {
			n++
		}
	}
	if len(pg.changes) == 0 {
		pg.changesTab.Text = T("Changes")
		pg.changesHead.SetText(T("Nothing to upload"))
		if pg.pending {
			pg.noneLabel().SetText(T("Your files are saved here but not on GitHub yet.\nClick \"Upload to GitHub\" to send them."))
		} else {
			pg.noneLabel().SetText(T("Everything is uploaded.\nChange files in the project folder, or drag new files into this window, and they will show up here."))
		}
		pg.changesNone.Show()
		pg.toggleAll.Hide()
	} else {
		pg.changesTab.Text = Tf("Changes (%d)", len(pg.changes))
		pg.changesHead.SetText(Tf("%d of %d files will be uploaded", n, len(pg.changes)))
		pg.changesNone.Hide()
		pg.toggleAll.Show()
		if n == len(pg.changes) {
			pg.toggleAll.SetText(T("Untick all"))
		} else {
			pg.toggleAll.SetText(T("Tick all"))
		}
	}
	pg.tabs.Refresh()
}

func (pg *projectPage) toggleAllFiles() {
	all := true
	for _, c := range pg.changes {
		all = all && pg.checked[c.Path]
	}
	for _, c := range pg.changes {
		pg.checked[c.Path] = !all
	}
	pg.changesList.Refresh()
	pg.updateHeader()
}

func (pg *projectPage) undo(c Change) {
	var title, msg, action string
	switch c.Kind {
	case kindNew:
		title, action = T("Remove this file?"), T("Delete file")
		msg = Tf("\"%s\" is not on GitHub yet. It will be deleted from your project folder.", c.Path)
	case kindRemoved:
		title, action = T("Bring this file back?"), T("Restore")
		msg = Tf("\"%s\" will be put back into your project folder, as it was at your last upload.", c.Path)
	default:
		title, action = T("Undo your changes?"), T("Undo changes")
		msg = Tf("\"%s\" will go back to how it was at your last upload. The changes you made to it will be lost.", c.Path)
	}
	confirmDanger(pg.u.win, title, msg, action, func() {
		if err := pg.proj.UndoChange(c.Path); err != nil {
			dialog.ShowInformation(T("Something went wrong"), friendlyError(err), pg.u.win)
		}
		pg.refresh()
	})
}

func (pg *projectPage) deleteFile(path string) {
	confirmDanger(pg.u.win, T("Delete this file?"),
		Tf("\"%s\" will be deleted from your project folder. If it is on GitHub, it will be removed there at your next upload.", path),
		T("Delete file"), func() {
			if err := pg.proj.DeleteFile(path); err != nil {
				dialog.ShowInformation(T("Something went wrong"), friendlyError(err), pg.u.win)
			}
			pg.refresh()
		})
}

func (pg *projectPage) onDrop(paths []string) {
	if pg.busy {
		return
	}
	root := pg.proj.Root
	var outside, clashes []string
	for _, p := range paths {
		if isInside(root, p) {
			continue
		}
		if isInside(p, root) {
			dialog.ShowInformation(T("Can't do that"), T("You can't put a folder inside itself."), pg.u.win)
			return
		}
		outside = append(outside, p)
		if _, err := os.Lstat(filepath.Join(root, filepath.Base(p))); err == nil {
			clashes = append(clashes, filepath.Base(p))
		}
	}
	if len(outside) == 0 {
		pg.refresh()
		return
	}
	if len(clashes) == 0 {
		pg.copyIn(outside, false)
		return
	}
	msg := Tf("Your project already has %s.\nReplace it with the new version, or keep both?", strings.Join(clashes, ", "))
	if len(clashes) > 3 {
		msg = Tf("Your project already has %d of these files.\nReplace them with the new versions, or keep both?", len(clashes))
	}
	dialog.ShowCustomConfirm(T("Replace files?"), T("Replace"), T("Keep both"), widget.NewLabel(msg), func(replace bool) {
		pg.copyIn(outside, replace)
	}, pg.u.win)
}

func (pg *projectPage) copyIn(paths []string, replace bool) {
	root := pg.proj.Root
	pg.setBusy(true, T("Adding files…"))
	go func() {
		var err error
		for _, p := range paths {
			if err = copyInto(p, root, replace); err != nil {
				break
			}
		}
		fyne.Do(func() {
			pg.setBusy(false, "")
			if err != nil {
				pg.status.SetText(T("Couldn't add a file: ") + friendlyError(err))
			} else {
				pg.status.SetText(Tf("Added %d item(s) to your project. Click Upload when you're ready.", len(paths)))
			}
			pg.tabs.SelectIndex(0)
			pg.refresh()
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
		return T("Update project")
	}
	names := make([]string, 0, 3)
	for _, p := range paths[:min(3, len(paths))] {
		names = append(names, filepath.Base(p))
	}
	t := T("Update") + " " + strings.Join(names, ", ")
	if len(paths) > 3 {
		t += Tf(" and %d more", len(paths)-3)
	}
	return t
}

func (pg *projectPage) upload() {
	if pg.proj == nil || pg.busy {
		return
	}
	if !pg.u.signedIn() {
		pg.status.SetText(T("Sign in to GitHub first, then click Upload again."))
		pg.u.showSignIn()
		return
	}
	var paths []string
	for _, c := range pg.changes {
		if pg.checked[c.Path] {
			paths = append(paths, c.Path)
		}
	}
	if len(paths) == 0 && !pg.pending && pg.proj.RemoteURL() != "" {
		pg.status.SetText(T("Nothing to upload. Tick the files you want to send first."))
		return
	}
	title := strings.TrimSpace(pg.message.Text)
	if title == "" {
		title = autoTitle(paths)
	}
	desc := pg.details.Text
	acc := *pg.u.current
	p := pg.proj
	repoInput := strings.TrimSpace(pg.repoName.Text)
	private := pg.private.Checked

	pg.setBusy(true, T("Getting ready…"))
	step := func(s string) { fyne.Do(func() { pg.status.SetText(s) }) }
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		created, err := ensureRemote(ctx, p, &acc, repoInput, private, step)
		var res *UploadResult
		if err == nil {
			res, err = p.Upload(ctx, &acc, paths, title, desc, step)
		}
		fyne.Do(func() {
			pg.setBusy(false, "")
			pg.updateRemoteInfo()
			defer pg.refresh()
			if err != nil {
				dialog.ShowInformation(T("Upload didn't finish"), friendlyError(err), pg.u.win)
				if errors.Is(err, transport.ErrRepositoryNotFound) {
					pg.showGone()
				}
				return
			}
			pg.showResult(p, res, created)
		})
	}()
}

func ensureRemote(ctx context.Context, p *Project, acc *Account, input string, private bool, step func(string)) (bool, error) {
	if p.RemoteURL() != "" {
		return false, nil
	}
	if i := strings.Index(input, "github.com"); i >= 0 {
		path := strings.Trim(strings.TrimSuffix(strings.TrimSpace(input[i+len("github.com"):]), ".git"), "/:")
		if strings.Count(path, "/") != 1 {
			return false, errors.New(T("that GitHub link doesn't look right; it should look like https://github.com/name/project"))
		}
		return false, p.SetRemote("https://github.com/" + path + ".git")
	}
	step(T("Creating your project on GitHub…"))
	repo, err := createOrFindRepo(ctx, acc, repoSlug(input), private)
	if err != nil {
		return false, err
	}
	return true, p.SetRemote(repo.CloneURL)
}

func (pg *projectPage) showResult(p *Project, res *UploadResult, created bool) {
	link := webURL(p.RemoteURL())
	switch {
	case res.SideBranch != "":
		if link != "" {
			link += "/tree/" + res.SideBranch
		}
		pg.u.resultDialog(T("Your work is safe"),
			Tf("Someone changed the same files on GitHub since your last upload, so your version was saved next to theirs as \"%s\" instead of replacing it.\n\nOpen GitHub to compare the two and pick what to keep.", res.SideBranch), link)
	case res.NothingToDo && !res.Merged:
		pg.status.SetText(T("Everything is already on GitHub. Nothing new to upload."))
	default:
		pg.message.SetText("")
		pg.details.SetText("")
		msg := T("Your changes are on GitHub.")
		if created {
			msg = T("Your project is now on GitHub.")
		}
		if res.Merged {
			msg += "\n" + T("Changes made on GitHub were also downloaded into your folder.")
		}
		pg.status.SetText(fmt.Sprintf("%s %s", T("Uploaded at"), time.Now().Format("15:04")))
		pg.u.resultDialog(T("Done!"), msg, link)
	}
}
