package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Set at build time with -ldflags "-X main.githubClientID=...". When empty the
// app falls back to asking for a personal access token.
var githubClientID = ""

var githubAPIBase = "https://api.github.com"

var httpClient = &http.Client{Timeout: 30 * time.Second}

var errBadToken = errors.New("github rejected the sign-in")

type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ghRepo struct {
	Name        string    `json:"name"`
	FullName    string    `json:"full_name"`
	HTMLURL     string    `json:"html_url"`
	CloneURL    string    `json:"clone_url"`
	Private     bool      `json:"private"`
	Description string    `json:"description"`
	PushedAt    time.Time `json:"pushed_at"`
}

// listRepos returns the projects the account owns or helps with, most recently
// updated first. Three pages cover everyone but very prolific users.
func listRepos(ctx context.Context, token string) ([]ghRepo, error) {
	var all []ghRepo
	for page := 1; page <= 3; page++ {
		var batch []ghRepo
		path := fmt.Sprintf("/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator&page=%d", page)
		code, err := githubAPI(ctx, token, "GET", path, nil, &batch)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf(T("GitHub answered with an error (%d). Try again later, or sign in again."), code)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func githubAPI(ctx context.Context, token, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPIBase+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "git-autopilot")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, errBadToken
	}
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func fetchUser(ctx context.Context, token string) (*ghUser, error) {
	var u ghUser
	code, err := githubAPI(ctx, token, "GET", "/user", nil, &u)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf(T("GitHub answered with an error (%d). Try again later, or sign in again."), code)
	}
	return &u, nil
}

// createOrFindRepo makes a new repository under the user's account, or returns
// the existing one when the name is already taken by that user.
func createOrFindRepo(ctx context.Context, acc *Account, name string, private bool) (*ghRepo, error) {
	var r ghRepo
	code, err := githubAPI(ctx, acc.Token, "POST", "/user/repos", map[string]any{
		"name":    name,
		"private": private,
	}, &r)
	if err != nil {
		return nil, err
	}
	switch code {
	case http.StatusCreated:
		return &r, nil
	case http.StatusUnprocessableEntity:
		code, err = githubAPI(ctx, acc.Token, "GET", "/repos/"+acc.Login+"/"+url.PathEscape(name), nil, &r)
		if err != nil {
			return nil, err
		}
		if code == http.StatusOK {
			return &r, nil
		}
		return nil, fmt.Errorf(T("the name %q can't be used on GitHub, try another one"), name)
	case http.StatusForbidden, http.StatusNotFound:
		return nil, errors.New(T("your GitHub key isn't allowed to create projects; make a new key with the \"repo\" box ticked"))
	}
	return nil, fmt.Errorf(T("GitHub answered with an error (%d). Try again later, or sign in again."), code)
}

type deviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func startDeviceLogin(ctx context.Context) (*deviceCode, error) {
	var dc deviceCode
	err := postForm(ctx, "https://github.com/login/device/code", url.Values{
		"client_id": {githubClientID},
		"scope":     {"repo"},
	}, &dc)
	if err != nil {
		return nil, err
	}
	if dc.DeviceCode == "" {
		return nil, errors.New(T("github sign-in is not available right now"))
	}
	return &dc, nil
}

// waitDeviceLogin polls until the user approves the code in the browser.
// GitHub asks clients to back off by 5s on "slow_down", per the device flow spec.
func waitDeviceLogin(ctx context.Context, dc *deviceCode) (string, error) {
	interval := time.Duration(max(dc.Interval, 5)) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		var res struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		err := postForm(ctx, "https://github.com/login/oauth/access_token", url.Values{
			"client_id":   {githubClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &res)
		if err != nil {
			continue
		}
		switch res.Error {
		case "":
			if res.AccessToken != "" {
				return res.AccessToken, nil
			}
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return "", errors.New(T("sign-in was cancelled"))
		default:
			return "", errors.New(T("the sign-in code expired, please try again"))
		}
	}
	return "", errors.New(T("the sign-in code expired, please try again"))
}
