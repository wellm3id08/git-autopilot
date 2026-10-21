package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func uploadAll(t *testing.T, p *Project, title string) *UploadResult {
	t.Helper()
	changes, err := p.Changes()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	acc := &Account{Login: "tester", Token: "unused", Email: "tester@example.com"}
	res, err := p.Upload(context.Background(), acc, paths, title, "", func(string) {})
	if err != nil {
		t.Fatalf("%s: %v", title, err)
	}
	return res
}

func TestUploadFlow(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(tmp, "a"), filepath.Join(tmp, "b")

	writeFile(t, filepath.Join(a, "one.txt"), "1")
	writeFile(t, filepath.Join(a, "dir", "two.txt"), "2")
	pa, err := openProject(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := pa.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, pa, "first upload")

	writeFile(t, filepath.Join(b, "three.txt"), "3")
	pb, err := openProject(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := pb.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, pb, "second computer joins")
	if got := readFile(t, filepath.Join(b, "dir", "two.txt")); got != "2" {
		t.Fatalf("existing GitHub files were not downloaded, got %q", got)
	}

	writeFile(t, filepath.Join(a, "one.txt"), "1a")
	if res := uploadAll(t, pa, "different files changed"); !res.Merged {
		t.Fatal("expected changes from both computers to be combined")
	}
	if got := readFile(t, filepath.Join(a, "three.txt")); got != "3" {
		t.Fatalf("combined file missing, got %q", got)
	}

	if res := uploadAll(t, pb, "nothing new"); !res.NothingToDo {
		t.Fatalf("expected nothing to do, got %+v", res)
	}
	if got := readFile(t, filepath.Join(b, "one.txt")); got != "1a" {
		t.Fatalf("latest version was not downloaded, got %q", got)
	}
	if changes, _ := pb.Changes(); len(changes) != 0 {
		t.Fatalf("folder should be clean, got %v", changes)
	}

	writeFile(t, filepath.Join(a, "one.txt"), "A")
	uploadAll(t, pa, "edit on a")
	writeFile(t, filepath.Join(b, "one.txt"), "B")
	if res := uploadAll(t, pb, "same file on b"); res.SideBranch == "" {
		t.Fatal("expected the conflicting version to go to a separate branch")
	}

	if err := os.Remove(filepath.Join(a, "dir", "two.txt")); err != nil {
		t.Fatal(err)
	}
	if res := uploadAll(t, pa, "delete a file"); res.NothingToDo {
		t.Fatal("deletion was not uploaded")
	}
}

func TestRemoteURLs(t *testing.T) {
	cases := map[string]string{
		"git@github.com:me/site.git":       "https://github.com/me/site",
		"ssh://git@github.com/me/site.git": "https://github.com/me/site",
		"https://github.com/me/site.git":   "https://github.com/me/site",
		"/some/local/path":                 "",
	}
	for in, want := range cases {
		if got := webURL(in); got != want {
			t.Errorf("webURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoSlug(t *testing.T) {
	cases := map[string]string{
		"My Cool Site!": "My-Cool-Site",
		"  ":            "my-project",
		"résumé 2026":   "r-sum-2026",
	}
	for in, want := range cases {
		if got := repoSlug(in); got != want {
			t.Errorf("repoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRelinkAfterGitHubProjectDeleted(t *testing.T) {
	tmp := t.TempDir()
	first, second := filepath.Join(tmp, "first.git"), filepath.Join(tmp, "second.git")
	for _, r := range []string{first, second} {
		if _, err := git.PlainInit(r, true); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "site")
	writeFile(t, filepath.Join(dir, "index.html"), "hi")
	p, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetRemote(first); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, p, "first")
	if p.Pending() {
		t.Fatal("nothing should be pending after upload")
	}

	if err := p.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if err := p.SetRemote(second); err != nil {
		t.Fatal(err)
	}
	if !p.Pending() {
		t.Fatal("a fresh GitHub project should need an upload")
	}
	if res := uploadAll(t, p, "again"); res.NothingToDo {
		t.Fatal("upload to the new project was skipped")
	}
	r, err := git.PlainOpen(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reference("refs/heads/main", true); err != nil {
		t.Fatal("new project did not receive the files")
	}
}

func TestSoleSubfolder(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "wrapper", "project", "a.txt"), "a")
	writeFile(t, filepath.Join(tmp, "wrapper", ".DS_Store"), "")
	if got := soleSubfolder(filepath.Join(tmp, "wrapper")); got != filepath.Join(tmp, "wrapper", "project") {
		t.Fatalf("got %q", got)
	}
	if got := soleSubfolder(filepath.Join(tmp, "wrapper", "project")); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/me/deleted" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = "https://api.github.com" }()

	p, err := openProject(filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("expected error for missing folder")
	}
	dir := t.TempDir()
	if p, err = openProject(dir); err != nil {
		t.Fatal(err)
	}
	acc := &Account{Login: "me", Token: "t"}
	for repo, want := range map[string]bool{"deleted": true, "alive": false} {
		if err := p.SetRemote("https://github.com/me/" + repo + ".git"); err != nil {
			t.Fatal(err)
		}
		if got := p.RemoteGone(context.Background(), acc); got != want {
			t.Errorf("%s: RemoteGone = %v, want %v", repo, got, want)
		}
	}
}
