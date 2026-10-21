package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var errConflict = errors.New("both copies changed the same files")

// integrate brings GitHub's version (remote) into the local branch (local).
//
// go-git cannot do real merges, so this only handles the safe case: GitHub and
// this computer changed different files. GitHub's files are written into the
// folder, then either the branch is moved forward (local had nothing new) or a
// merge commit with both parents is created. Any overlap, including files the
// user changed but didn't tick, returns errConflict and nothing is touched.
func (p *Project) integrate(wt *git.Worktree, local, remote plumbing.Hash, sig *object.Signature) (bool, error) {
	if local == remote {
		return false, nil
	}
	lc, err := p.repo.CommitObject(local)
	if err != nil {
		return false, err
	}
	rc, err := p.repo.CommitObject(remote)
	if err != nil {
		return false, err
	}
	if ok, err := rc.IsAncestor(lc); err != nil || ok {
		return false, err
	}
	bases, err := lc.MergeBase(rc)
	if err != nil {
		return false, err
	}
	if len(bases) == 0 {
		return false, errConflict
	}
	base := bases[0]
	behind := base.Hash == lc.Hash

	baseTree, err := base.Tree()
	if err != nil {
		return false, err
	}
	localTree, err := lc.Tree()
	if err != nil {
		return false, err
	}
	remoteTree, err := rc.Tree()
	if err != nil {
		return false, err
	}

	touched := map[string]bool{}
	localChanges, err := object.DiffTree(baseTree, localTree)
	if err != nil {
		return false, err
	}
	for _, ch := range localChanges {
		touched[ch.From.Name] = true
		touched[ch.To.Name] = true
	}
	st, err := wt.Status()
	if err != nil {
		return false, err
	}
	for path, s := range st {
		if s.Worktree != git.Unmodified || s.Staging != git.Unmodified {
			touched[path] = true
		}
	}

	remoteChanges, err := object.DiffTree(baseTree, remoteTree)
	if err != nil {
		return false, err
	}
	var paths []string
	for _, ch := range remoteChanges {
		for _, name := range []string{ch.From.Name, ch.To.Name} {
			if name == "" {
				continue
			}
			if touched[name] {
				return false, errConflict
			}
			paths = append(paths, name)
		}
	}
	for _, ch := range remoteChanges {
		if ch.To.Name == "" {
			continue
		}
		m := ch.To.TreeEntry.Mode
		if m != filemode.Regular && m != filemode.Executable && m != filemode.Deprecated {
			return false, errConflict
		}
	}

	for _, ch := range remoteChanges {
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			if err := os.Remove(filepath.Join(p.Root, filepath.FromSlash(ch.From.Name))); err != nil && !os.IsNotExist(err) {
				return false, err
			}
		}
		if ch.To.Name != "" {
			if err := writeFromTree(p.Root, remoteTree, ch.To.Name); err != nil {
				return false, err
			}
		}
	}

	if behind {
		if err := wt.Reset(&git.ResetOptions{Commit: remote, Mode: git.MixedReset}); err != nil {
			return false, err
		}
		return false, nil
	}
	for _, path := range paths {
		if _, err := wt.Add(path); err != nil {
			return false, err
		}
	}
	_, err = wt.Commit("Combine changes from GitHub", &git.CommitOptions{
		Author:  sig,
		Parents: []plumbing.Hash{local, remote},
	})
	return true, err
}

func writeFromTree(root string, tree *object.Tree, path string) error {
	f, err := tree.File(path)
	if err != nil {
		return err
	}
	r, err := f.Reader()
	if err != nil {
		return err
	}
	defer r.Close()
	dst := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if f.Mode == filemode.Executable {
		perm = 0o755
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
