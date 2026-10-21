package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
)

const (
	prefProjects    = "projects"
	prefLastProject = "lastProject"
	prefHidden      = "hiddenAccounts"
)

// The library is the list of folders on this computer the user has worked on.
func loadLibrary(p fyne.Preferences) []string {
	var paths []string
	_ = json.Unmarshal([]byte(p.String(prefProjects)), &paths)
	if last := p.String(prefLastProject); last != "" {
		paths = append(paths, last)
		p.RemoveValue(prefLastProject)
	}
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			continue
		}
		seen[key] = true
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			out = append(out, path)
		}
	}
	saveLibrary(p, out)
	return out
}

func saveLibrary(p fyne.Preferences, paths []string) {
	b, _ := json.Marshal(paths)
	p.SetString(prefProjects, string(b))
}

func addToLibrary(p fyne.Preferences, path string) {
	paths := loadLibrary(p)
	for _, existing := range paths {
		if strings.EqualFold(filepath.Clean(existing), filepath.Clean(path)) {
			return
		}
	}
	saveLibrary(p, append([]string{path}, paths...))
}

func removeFromLibrary(p fyne.Preferences, path string) {
	var keep []string
	for _, existing := range loadLibrary(p) {
		if !strings.EqualFold(filepath.Clean(existing), filepath.Clean(path)) {
			keep = append(keep, existing)
		}
	}
	saveLibrary(p, keep)
}

func hiddenAccounts(p fyne.Preferences) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(p.String(prefHidden), ",") {
		if l != "" {
			out[strings.ToLower(l)] = true
		}
	}
	return out
}

func setAccountHidden(p fyne.Preferences, login string, hidden bool) {
	set := hiddenAccounts(p)
	if hidden {
		set[strings.ToLower(login)] = true
	} else {
		delete(set, strings.ToLower(login))
	}
	var list []string
	for l := range set {
		list = append(list, l)
	}
	p.SetString(prefHidden, strings.Join(list, ","))
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Git Autopilot")
}

// freeFolder returns base, or "base (2)", "base (3)"... whichever doesn't exist.
func freeFolder(parent, name string) string {
	dest := filepath.Join(parent, name)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		return dest
	}
	return filepath.Join(parent, freeName(parent, name))
}
