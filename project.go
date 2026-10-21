package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const maxGitHubFileSize = 100 << 20

var (
	errNotSignedIn = errors.New("please sign in to GitHub first")
	errNoRemote    = errors.New("this folder isn't linked to a GitHub project yet")
)

const (
	kindNew     = "New"
	kindChanged = "Changed"
	kindRemoved = "Removed"
)

type Change struct {
	Path string
	Kind string
}

type Project struct {
	Root string
	repo *git.Repository
}

type UploadResult struct {
	NothingToDo bool
	Merged      bool
	SideBranch  string
}

func openProject(path string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, errors.New(T("that isn't a folder"))
	}
	if tooBroad(abs) {
		return nil, errors.New(T("that folder is too big to be one project; pick the folder that holds just your project"))
	}
	repo, err := git.PlainOpenWithOptions(abs, &git.PlainOpenOptions{DetectDotGit: false})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInitWithOptions(abs, &git.PlainInitOptions{
			InitOptions: git.InitOptions{DefaultBranch: plumbing.Main},
		})
	}
	if err != nil {
		return nil, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	return &Project{Root: wt.Filesystem.Root(), repo: repo}, nil
}

func tooBroad(abs string) bool {
	if filepath.Dir(abs) == abs {
		return true
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	for _, p := range []string{home, filepath.Join(home, "Desktop"), filepath.Join(home, "Documents"), filepath.Join(home, "Downloads")} {
		if strings.EqualFold(filepath.Clean(p), abs) {
			return true
		}
	}
	return false
}

func (p *Project) Name() string { return filepath.Base(p.Root) }

func (p *Project) Changes() ([]Change, error) {
	wt, err := p.repo.Worktree()
	if err != nil {
		return nil, err
	}
	st, err := wt.Status()
	if err != nil {
		return nil, err
	}
	var out []Change
	for path, s := range st {
		if s.Worktree == git.Unmodified && s.Staging == git.Unmodified {
			continue
		}
		kind := kindChanged
		switch {
		case s.Worktree == git.Deleted || s.Staging == git.Deleted:
			kind = kindRemoved
		case s.Worktree == git.Untracked || s.Staging == git.Added:
			kind = kindNew
		}
		out = append(out, Change{Path: path, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (p *Project) branch() plumbing.ReferenceName {
	head, err := p.repo.Storer.Reference(plumbing.HEAD)
	if err == nil && head.Type() == plumbing.SymbolicReference {
		return head.Target()
	}
	return plumbing.Main
}

func (p *Project) RemoteURL() string {
	r, err := p.repo.Remote("origin")
	if err != nil || len(r.Config().URLs) == 0 {
		return ""
	}
	return r.Config().URLs[0]
}

func (p *Project) SetRemote(url string) error {
	if err := p.Disconnect(); err != nil {
		return err
	}
	_, err := p.repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}})
	return err
}

// Disconnect forgets the GitHub link together with everything remembered about
// that project. Stale origin/* refs would otherwise make a new, empty GitHub
// project look as if it already had all the files.
func (p *Project) Disconnect() error {
	if err := p.repo.DeleteRemote("origin"); err != nil && !errors.Is(err, git.ErrRemoteNotFound) {
		return err
	}
	refs, err := p.repo.References()
	if err != nil {
		return err
	}
	var stale []plumbing.ReferenceName
	_ = refs.ForEach(func(r *plumbing.Reference) error {
		if strings.HasPrefix(r.Name().String(), "refs/remotes/origin/") {
			stale = append(stale, r.Name())
		}
		return nil
	})
	for _, name := range stale {
		if err := p.repo.Storer.RemoveReference(name); err != nil {
			return err
		}
	}
	return nil
}

// Pending reports whether there are saved versions GitHub doesn't have yet.
func (p *Project) Pending() bool {
	head, err := p.repo.Head()
	if err != nil {
		return false
	}
	if p.RemoteURL() == "" {
		return true
	}
	r, err := p.repo.Reference(plumbing.NewRemoteReferenceName("origin", head.Name().Short()), true)
	return err != nil || r.Hash() != head.Hash()
}

// RemoteGone reports whether the linked GitHub project is missing (deleted, or
// not visible to this account). Network problems count as "not gone".
func (p *Project) RemoteGone(ctx context.Context, acc *Account) bool {
	web := webURL(p.RemoteURL())
	path, ok := strings.CutPrefix(web, "https://github.com/")
	if !ok || acc == nil || acc.Token == "" || strings.Count(path, "/") != 1 {
		return false
	}
	code, err := githubAPI(ctx, acc.Token, "GET", "/repos/"+path, nil, nil)
	return err == nil && code == 404
}

var sshRemote = regexp.MustCompile(`^(?:ssh://)?git@github\.com[:/](.+?)(?:\.git)?/?$`)

// httpsURL rewrites SSH remotes so a token can be used for them.
func httpsURL(remote string) string {
	if m := sshRemote.FindStringSubmatch(remote); m != nil {
		return "https://github.com/" + m[1] + ".git"
	}
	return remote
}

func webURL(remote string) string {
	u := strings.TrimSuffix(httpsURL(remote), ".git")
	if strings.HasPrefix(u, "https://") {
		return u
	}
	return ""
}

func (p *Project) checkSizes(paths []string) error {
	for _, rel := range paths {
		fi, err := os.Stat(filepath.Join(p.Root, filepath.FromSlash(rel)))
		if err == nil && fi.Size() > maxGitHubFileSize {
			return fmt.Errorf(T("%q is bigger than 100 MB, which GitHub does not accept. Untick it or make it smaller"), rel)
		}
	}
	return nil
}

// Upload saves the ticked files as a new version and sends it to GitHub. It
// never discards work: if the GitHub copy changed in a way that can't be
// combined automatically, the new version goes to a separate branch instead.
// fetch downloads GitHub's history without touching the folder, except for a
// brand new folder linked to a project that already has files (adoptRemote).
func (p *Project) fetch(ctx context.Context, wt *git.Worktree, auth transport.AuthMethod, remoteURL string) error {
	if remoteURL == "" {
		return errNoRemote
	}
	err := p.repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		Auth:       auth,
		RefSpecs:   []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return err
	}
	if _, err := p.repo.Head(); errors.Is(err, plumbing.ErrReferenceNotFound) {
		return p.adoptRemote(wt)
	}
	return nil
}

func (p *Project) Upload(ctx context.Context, acc *Account, paths []string, title, desc string, step func(string)) (*UploadResult, error) {
	if acc == nil || acc.Token == "" {
		return nil, errNotSignedIn
	}
	if err := p.checkSizes(paths); err != nil {
		return nil, err
	}
	wt, err := p.repo.Worktree()
	if err != nil {
		return nil, err
	}
	auth := &githttp.BasicAuth{Username: acc.Login, Password: acc.Token}
	remoteURL := httpsURL(p.RemoteURL())
	sig := func() *object.Signature {
		return &object.Signature{Name: acc.authorName(), Email: acc.Email, When: time.Now()}
	}

	step(T("Checking GitHub for changes…"))
	if err := p.fetch(ctx, wt, auth, remoteURL); err != nil {
		return nil, err
	}
	branch := p.branch()
	remoteRef := plumbing.NewRemoteReferenceName("origin", branch.Short())

	step(T("Saving your changes…"))
	for _, path := range paths {
		if _, err := wt.Add(path); err != nil {
			return nil, fmt.Errorf(T("could not include %q: %w"), path, err)
		}
	}
	msg := strings.TrimSpace(title)
	if d := strings.TrimSpace(desc); d != "" {
		msg += "\n\n" + d
	}
	_, err = wt.Commit(msg, &git.CommitOptions{Author: sig()})
	if err != nil && !errors.Is(err, git.ErrEmptyCommit) {
		return nil, err
	}

	res := &UploadResult{}
	head, err := p.repo.Head()
	if err != nil {
		return &UploadResult{NothingToDo: true}, nil
	}
	if rref, err := p.repo.Reference(remoteRef, true); err == nil {
		merged, err := p.integrate(wt, head.Hash(), rref.Hash(), sig())
		switch {
		case errors.Is(err, errConflict):
			return p.pushSideBranch(ctx, auth, remoteURL, head.Hash(), step)
		case err != nil:
			return nil, err
		}
		res.Merged = merged
		head, _ = p.repo.Head()
		if head.Hash() == rref.Hash() {
			res.NothingToDo = !merged
			return res, nil
		}
	}

	step(T("Uploading to GitHub…"))
	err = p.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		Auth:       auth,
		RefSpecs:   []config.RefSpec{config.RefSpec(branch + ":" + branch)},
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		res.NothingToDo = true
		return res, nil
	}
	if err != nil && (errors.Is(err, git.ErrForceNeeded) || strings.Contains(err.Error(), "non-fast-forward")) {
		return p.pushSideBranch(ctx, auth, remoteURL, head.Hash(), step)
	}
	return res, err
}

// adoptRemote handles a brand new local folder linked to a GitHub project that
// already has history: the local branch starts from GitHub's latest version,
// local files are left untouched and show up as changes on top of it, and files
// that only exist on GitHub are downloaded.
func (p *Project) adoptRemote(wt *git.Worktree) error {
	branch := p.branch()
	var rref *plumbing.Reference
	for _, name := range []string{branch.Short(), "main", "master"} {
		if r, err := p.repo.Reference(plumbing.NewRemoteReferenceName("origin", name), true); err == nil {
			rref = r
			branch = plumbing.NewBranchReferenceName(name)
			break
		}
	}
	if rref == nil {
		return nil
	}
	s := p.repo.Storer
	if err := s.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch)); err != nil {
		return err
	}
	if err := s.SetReference(plumbing.NewHashReference(branch, rref.Hash())); err != nil {
		return err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: rref.Hash(), Mode: git.MixedReset}); err != nil {
		return err
	}
	commit, err := p.repo.CommitObject(rref.Hash())
	if err != nil {
		return err
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	st, err := wt.Status()
	if err != nil {
		return err
	}
	for path, fs := range st {
		if fs.Worktree == git.Deleted {
			if err := writeFromTree(p.Root, tree, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Project) pushSideBranch(ctx context.Context, auth transport.AuthMethod, remoteURL string, h plumbing.Hash, step func(string)) (*UploadResult, error) {
	step(T("Uploading your version separately…"))
	name := "my-changes-" + time.Now().Format("2006-01-02-150405")
	ref := plumbing.NewBranchReferenceName(name)
	if err := p.repo.Storer.SetReference(plumbing.NewHashReference(ref, h)); err != nil {
		return nil, err
	}
	err := p.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		Auth:       auth,
		RefSpecs:   []config.RefSpec{config.RefSpec(ref + ":" + ref)},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil, err
	}
	return &UploadResult{SideBranch: name}, nil
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case errors.Is(err, errBadToken),
		errors.Is(err, transport.ErrAuthenticationRequired),
		errors.Is(err, transport.ErrAuthorizationFailed):
		return T("GitHub didn't accept your sign-in. Please add your account again.")
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return T("GitHub can't find this project, or your account isn't allowed to change it.")
	case errors.Is(err, errNotSignedIn), errors.Is(err, errNoRemote), errors.Is(err, errConflict):
		return T(capitalize(s))
	case strings.Contains(s, "no such host"), strings.Contains(s, "dial tcp"),
		strings.Contains(s, "timeout"), strings.Contains(s, "connection refused"),
		strings.Contains(s, "network is unreachable"):
		return T("Can't reach GitHub. Check your internet connection and try again.")
	}
	return capitalize(s)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
