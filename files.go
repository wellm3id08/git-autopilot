package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func isInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// freeName returns "name (2).ext", "name (3).ext"... for the first unused name.
func freeName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

func copyInto(src, dstDir string, replace bool) error {
	name := filepath.Base(src)
	dst := filepath.Join(dstDir, name)
	if _, err := os.Lstat(dst); err == nil && !replace {
		dst = filepath.Join(dstDir, freeName(dstDir, name))
	}
	return copyPath(src, dst)
}

func copyPath(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil
	case fi.IsDir():
		if fi.Name() == ".git" {
			return nil
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// soleSubfolder returns the only visible item inside dir when that item is a
// folder, ignoring hidden files and OS clutter.
func soleSubfolder(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var only os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "__MACOSX" || strings.EqualFold(name, "desktop.ini") || strings.EqualFold(name, "Thumbs.db") {
			continue
		}
		if only != nil {
			return ""
		}
		only = e
	}
	if only == nil || !only.IsDir() {
		return ""
	}
	return filepath.Join(dir, only.Name())
}
