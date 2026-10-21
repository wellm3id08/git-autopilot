<p align="center">
  <img src="assets/icon.png" width="96" alt="Git Autopilot icon">
</p>

<h1 align="center">Git Autopilot</h1>

<p align="center">
  Put your projects on GitHub without ever opening a terminal.
</p>

You made a website, a school assignment, a game mod, a pile of design files. Someone told you to "just push it to GitHub", and then the instructions started talking about branches, remotes, staging and SSH keys.

Git Autopilot skips all of that. You drag your folder in, write a sentence about what you changed, and press one button. Your files end up on GitHub. That's it.

It's a single file you download and double-click. Nothing to install, and you don't need Git on your computer.

## What it looks like in practice

1. **Sign in.** If you've ever used GitHub on this computer, Git Autopilot usually finds your account by itself. If not, click **Add account** and follow the three short steps.
2. **Drop your project folder** into the window (or click **Choose folder**).
3. **Look at the list.** It shows every file that's new, changed or removed since your last upload. Untick anything you don't want to send.
4. **Write what you changed**, like "Fixed the typo on the contact page". You can leave it empty and Git Autopilot will write something sensible for you.
5. **Click Upload to GitHub.**

The first time you upload a folder, Git Autopilot creates the project on GitHub for you (private by default). After that, every upload just adds your newest changes.

Want to add files from somewhere else on your computer? Drag them into the window and they're copied into your project. If a file with the same name already exists, you choose whether to replace it or keep both.

## Download

Grab the latest version from the [Releases page](../../releases/latest).

| Your computer | Download | How to open it |
| --- | --- | --- |
| Windows 10 / 11 | `Git-Autopilot-…-windows-amd64.exe` | Double-click it. If Windows SmartScreen warns you, click **More info → Run anyway**. |
| Linux | `Git-Autopilot-…-x86_64.AppImage` | Right-click → Properties → allow running as a program, then double-click. Or run `chmod +x Git-Autopilot-*.AppImage`. |

## Questions people actually ask

**Do I need to install Git?**
No. Everything Git normally does is built into the app.

**I made changes on the GitHub website. Will uploading erase them?**
No. Before every upload, Git Autopilot checks GitHub first. If the website changes touched different files than yours, both sets are combined and the website changes are downloaded into your folder.

**What if someone changed the *same* file I did?**
Git Autopilot never overwrites anyone's work. Your version is uploaded next to theirs under a name like `my-changes-2026-10-20-153012`, and the app gives you a link to compare the two on GitHub. Pick the one you want there.

**I already have this project on GitHub. Can I connect my folder to it?**
Yes. When a folder isn't on GitHub yet, the app shows a name box. Paste the link to your existing GitHub project instead of a name, then upload. Any files that are on GitHub but missing from your folder are downloaded for you; your own files are kept as they are.

**Why won't it upload my video file?**
GitHub refuses single files larger than 100 MB. The app tells you which file is too big so you can untick it.

**Where is my sign-in kept?**
In the app's settings file on your computer:

- Windows: `%AppData%\fyne\io.github.gitautopilot\preferences.json`
- Linux: `~/.config/fyne/io.github.gitautopilot/preferences.json`

It isn't encrypted, so treat that file like a password. To sign out, delete it. You can also revoke the key at any time from [github.com/settings/tokens](https://github.com/settings/tokens) or [github.com/settings/applications](https://github.com/settings/applications).

**Where does it look for existing accounts?**
The Git credential manager (if Git is installed), the GitHub CLI, `~/.git-credentials`, and the `GH_TOKEN` / `GITHUB_TOKEN` environment variables. Keys that GitHub no longer accepts are skipped.

**I deleted the project on GitHub (or uploaded the wrong thing). How do I start over?**
Open the folder in Git Autopilot. If the GitHub project no longer exists, the app says so and offers **Upload it as a new project**. To move a folder to a different GitHub project on purpose, click the **⋮** button next to **Choose folder** and pick **Upload to a different GitHub project**. Nothing is deleted on your computer or on GitHub.

**I picked the wrong folder by accident.**
Click **Choose folder** and pick the right one. If you choose a folder that only contains another folder (which is what unzipping a download often gives you), the app asks whether you meant the folder inside.

**Something went wrong halfway through. Did I lose anything?**
No. Your files are never deleted by an upload. Fix whatever the message says (usually the internet connection) and click Upload again.

## For developers

Git Autopilot is written in Go, using [Fyne](https://fyne.io) for the interface and [go-git](https://github.com/go-git/go-git) for everything Git-related, so the result is one self-contained binary.

### Building

You need Go (see `go.mod` for the version) and a C compiler.

On Debian/Ubuntu:

```sh
sudo apt install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev wayland-protocols
go build -o git-autopilot .
```

On Windows, install a MinGW-w64 GCC (for example from [winlibs.com](https://winlibs.com)), then:

```sh
go build -ldflags "-H=windowsgui" -o git-autopilot.exe .
```

Run the tests with `go test ./...`. They exercise the full upload flow, including combining changes and the separate-branch fallback, against a local repository, so no network is needed.

### Enabling "Sign in with GitHub"

Out of the box the app asks people to paste a personal access token, with a button that opens the right GitHub page pre-filled. If you want the smoother browser sign-in instead:

1. Create an OAuth App at [github.com/settings/developers](https://github.com/settings/developers). Any homepage and callback URL will do.
2. Tick **Enable Device Flow** and save.
3. In your fork, go to **Settings → Secrets and variables → Actions → Variables** and add `GH_OAUTH_CLIENT_ID` with the app's Client ID.

The release workflow bakes it in. For local builds, pass it yourself:

```sh
go build -ldflags "-X main.githubClientID=YOUR_CLIENT_ID" -o git-autopilot .
```

The Client ID is not a secret; it's safe to ship in the binary.

### Releasing

Every push and pull request builds Windows and Linux binaries as workflow artifacts. To publish a release, tag a commit:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The workflow builds the `.exe` and the AppImage and attaches them to a new GitHub release.

### Project layout

| File | What's in it |
| --- | --- |
| `main.go` | Startup |
| `ui.go` | The window, drag and drop, sign-in dialogs |
| `project.go` | Opening folders, listing changes, the upload sequence |
| `merge.go` | Combining GitHub's changes with local ones |
| `accounts.go` | Finding existing GitHub sign-ins on the machine |
| `github.go` | GitHub API calls and the device sign-in flow |
| `files.go` | Copying dropped files into a project |

## License

MIT. See [LICENSE](LICENSE).
