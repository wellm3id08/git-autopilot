package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const maxListedFiles = 5000

type Version struct {
	Title  string
	Author string
	When   time.Time
}

// Download copies a GitHub project into dest, which must not exist yet.
func Download(ctx context.Context, acc *Account, repo *ghRepo, dest string) (*Project, error) {
	opts := &git.CloneOptions{URL: repo.CloneURL}
	if acc != nil && acc.Token != "" {
		opts.Auth = &githttp.BasicAuth{Username: acc.Login, Password: acc.Token}
	}
	_, err := git.PlainCloneContext(ctx, dest, false, opts)
	// The project's default branch may point at a branch that doesn't exist
	// (e.g. "master" while uploads went to "main"); try the usual names.
	for _, b := range []plumbing.ReferenceName{plumbing.Main, plumbing.Master} {
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			break
		}
		_ = os.RemoveAll(dest)
		opts.ReferenceName = b
		_, err = git.PlainCloneContext(ctx, dest, false, opts)
	}
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		_ = os.RemoveAll(dest)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return nil, err
		}
		p, err := openProject(dest)
		if err != nil {
			return nil, err
		}
		return p, p.SetRemote(repo.CloneURL)
	}
	if err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	return openProject(dest)
}

// Sync brings changes made on GitHub into the folder without uploading
// anything. Files the user is editing are never overwritten; if GitHub changed
// one of them, errConflict is returned and the folder is left alone.
func (p *Project) Sync(ctx context.Context, acc *Account) (bool, error) {
	if acc == nil || acc.Token == "" {
		return false, errNotSignedIn
	}
	wt, err := p.repo.Worktree()
	if err != nil {
		return false, err
	}
	auth := &githttp.BasicAuth{Username: acc.Login, Password: acc.Token}
	before, _ := p.repo.Head()
	if err := p.fetch(ctx, wt, auth, httpsURL(p.RemoteURL())); err != nil {
		return false, err
	}
	head, err := p.repo.Head()
	if err != nil {
		return false, nil
	}
	rref, err := p.repo.Reference(plumbing.NewRemoteReferenceName("origin", head.Name().Short()), true)
	if err != nil {
		return false, nil
	}
	lc, err := p.repo.CommitObject(head.Hash())
	if err != nil {
		return false, err
	}
	rc, err := p.repo.CommitObject(rref.Hash())
	if err != nil {
		return false, err
	}
	if behind, err := lc.IsAncestor(rc); err != nil || !behind {
		// Local work GitHub doesn't have yet gets combined on the next upload.
		return before == nil || before.Hash() != head.Hash(), err
	}
	sig := &object.Signature{Name: acc.authorName(), Email: acc.Email, When: time.Now()}
	if _, err := p.integrate(wt, head.Hash(), rref.Hash(), sig); err != nil {
		return false, err
	}
	return head.Hash() != rref.Hash(), nil
}

// UndoChange puts one file back the way it was at the last upload. A file
// that was never uploaded is deleted.
func (p *Project) UndoChange(path string) error {
	full := filepath.Join(p.Root, filepath.FromSlash(path))
	head, err := p.repo.Head()
	if err != nil {
		return os.RemoveAll(full)
	}
	c, err := p.repo.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	tree, err := c.Tree()
	if err != nil {
		return err
	}
	if _, err := tree.File(path); errors.Is(err, object.ErrFileNotFound) {
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if err := writeFromTree(p.Root, tree, path); err != nil {
		return err
	}
	wt, err := p.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.MixedReset, Files: []string{path}})
}

func (p *Project) DeleteFile(path string) error {
	return os.Remove(filepath.Join(p.Root, filepath.FromSlash(path)))
}

// Files lists everything in the folder except Git's own data, capped so a huge
// folder can't freeze the window.
func (p *Project) Files() ([]string, error) {
	var out []string
	err := filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(p.Root, path)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= maxListedFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func (p *Project) History(limit int) ([]Version, error) {
	head, err := p.repo.Head()
	if err != nil {
		return nil, nil
	}
	iter, err := p.repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []Version
	for len(out) < limit {
		c, err := iter.Next()
		if err != nil {
			break
		}
		title, _, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
		out = append(out, Version{Title: title, Author: c.Author.Name, When: c.Author.When})
	}
	return out, nil
}

// FullName returns "owner/name" for projects linked to GitHub.
func (p *Project) FullName() string {
	path, ok := strings.CutPrefix(webURL(p.RemoteURL()), "https://github.com/")
	if !ok {
		return ""
	}
	return path
}
