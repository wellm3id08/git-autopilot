package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/go-git/go-git/v5/config"
)

type Account struct {
	Login  string `json:"login"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Token  string `json:"token"`
	Source string `json:"source"`
}

func (a *Account) Label() string {
	if a.Token == "" {
		return a.Name + " " + T("(not signed in)")
	}
	if a.Name != "" && a.Name != a.Login {
		return a.Name + " (@" + a.Login + ")"
	}
	return "@" + a.Login
}

func (a *Account) authorName() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Login
}

const prefAccounts = "accounts"

func loadSavedAccounts(p fyne.Preferences) []Account {
	var accs []Account
	_ = json.Unmarshal([]byte(p.String(prefAccounts)), &accs)
	return accs
}

func saveAccounts(p fyne.Preferences, accs []Account) {
	var keep []Account
	for _, a := range accs {
		if a.Token != "" && a.Source == "Signed in here" {
			keep = append(keep, a)
		}
	}
	b, _ := json.Marshal(keep)
	p.SetString(prefAccounts, string(b))
}

func accountFromToken(ctx context.Context, token, source string) (*Account, error) {
	u, err := fetchUser(ctx, token)
	if err != nil {
		return nil, err
	}
	email := u.Email
	if email == "" {
		email = fmt.Sprintf("%d+%s@users.noreply.github.com", u.ID, u.Login)
	}
	return &Account{Login: u.Login, Name: u.Name, Email: email, Token: token, Source: source}, nil
}

// detectAccounts looks everywhere a GitHub sign-in commonly lives on a machine
// and keeps only the tokens GitHub still accepts.
func detectAccounts(ctx context.Context, saved []Account) []Account {
	type cand struct{ token, source string }
	var cands []cand
	seen := map[string]bool{}
	add := func(token, source string) {
		token = strings.TrimSpace(token)
		if token != "" && !seen[token] {
			seen[token] = true
			cands = append(cands, cand{token, source})
		}
	}
	for _, a := range saved {
		add(a.Token, "Signed in here")
	}
	add(os.Getenv("GH_TOKEN"), "Environment")
	add(os.Getenv("GITHUB_TOKEN"), "Environment")
	for _, t := range gitCredentialFileTokens() {
		add(t, "Git credentials file")
	}
	for _, t := range ghCLITokens() {
		add(t, "GitHub CLI")
	}
	if t := gitCredentialHelperToken(ctx); t != "" {
		add(t, "Git credential manager")
	}

	results := make([]*Account, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a, err := accountFromToken(ctx, c.token, c.source); err == nil {
				results[i] = a
			}
		}()
	}
	wg.Wait()

	var out []Account
	logins := map[string]bool{}
	for _, a := range results {
		if a != nil && !logins[strings.ToLower(a.Login)] {
			logins[strings.ToLower(a.Login)] = true
			out = append(out, *a)
		}
	}
	if len(out) == 0 {
		if name, email := gitIdentity(); name != "" {
			out = append(out, Account{Name: name, Email: email, Source: "Git settings"})
		}
	}
	return out
}

func gitIdentity() (string, string) {
	cfg, err := config.LoadConfig(config.GlobalScope)
	if err != nil {
		return "", ""
	}
	return cfg.User.Name, cfg.User.Email
}

func gitCredentialFileTokens() []string {
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".git-credentials")}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		paths = append(paths, filepath.Join(x, "git", "credentials"))
	} else {
		paths = append(paths, filepath.Join(home, ".config", "git", "credentials"))
	}
	var out []string
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			u, err := url.Parse(strings.TrimSpace(sc.Text()))
			if err != nil || u.Host != "github.com" || u.User == nil {
				continue
			}
			if pw, ok := u.User.Password(); ok {
				out = append(out, pw)
			}
		}
		f.Close()
	}
	return out
}

// ghCLITokens reads hosts.yml written by the GitHub CLI. Newer gh versions keep
// the token in the OS keyring instead, which the credential helper path covers.
func ghCLITokens() []string {
	dir := os.Getenv("GH_CONFIG_DIR")
	if dir == "" {
		if runtime.GOOS == "windows" {
			dir = filepath.Join(os.Getenv("AppData"), "GitHub CLI")
		} else if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "gh")
		} else {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".config", "gh")
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	if err != nil {
		return nil
	}
	var out []string
	inGitHub := false
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			inGitHub = strings.HasPrefix(line, "github.com:")
			continue
		}
		t := strings.TrimSpace(line)
		if inGitHub && strings.HasPrefix(t, "oauth_token:") {
			out = append(out, strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "oauth_token:")), `"'`))
		}
	}
	return out
}

// gitCredentialHelperToken asks an installed Git for its stored GitHub login.
// This reaches Windows Credential Manager, macOS Keychain and libsecret without
// linking against any of them. Interactive prompts are disabled so a missing
// credential never pops up a window.
func gitCredentialHelperToken(ctx context.Context) string {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitPath, "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if pw, ok := strings.CutPrefix(strings.TrimSpace(line), "password="); ok {
			return pw
		}
	}
	return ""
}
