package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim/install"
)

// version is the release this build is, as "v0.1.0", set by the release
// workflow with -ldflags "-X main.version=…". A build from a working tree,
// as the test kit's, is "dev": it runs where it is, with no installer
// and no updates.
var version = "dev"

// releaseTagPrefix starts the tags of this program's releases, which sit
// in marrasen/marraw beside the Electron app's: "gunim-v0.1.0". Electron's
// updater skips tags that are no plain version, and its release workflow
// starts only on v* tags.
const releaseTagPrefix = "gunim-"

// updateKey is the public key this program's releases are signed with,
// as gunimsign printed it; the private key is the GUNIM_SIGN_KEY secret.
const updateKey = "kDs3Mg12zatGTlzHWH4wGkfCHzukkewOcY7FVpvvCkQ="

// ortFiles are ONNX Runtime's libraries, kept in a release build to put
// beside the program as it installs; nil in other builds, which find the
// library as marrawd does.
var ortFiles fs.FS

// installApp is this program as gunim's installer installs and updates
// it.
func installApp() install.App {
	return install.App{
		Name:        "marraw (gunim test)",
		ID:          "marraw-gunim",
		Exe:         "marraw-gunim",
		Version:     version,
		Publisher:   "Marcus Johansson",
		Description: "marraw's photo culling and editing, drawn with gunim: a test build beside the Electron app.",
		URL:         "https://github.com/marrasen/marraw",
		IconFunc:    bigIcon,
		Files:       ortFiles,
		Space:       1820 << 20, // every AI model, if all are fetched; the preview cache has a limit of its own
		Updates:     gunimReleases{Repo: "marrasen/marraw"},
		UpdateKey:   updateKey,
		// Updates ask first: the user is told of a release and chooses
		// when to take it, never finding one put in place unasked.
		UpdateMode: install.UpdatesNotify,
		Available: func(r install.Release) {
			if f := updateAvailable; f != nil {
				f(r)
			}
		},
		Updated: func(r install.Release) {
			if f := updateReady; f != nil {
				f(r)
			}
		},
		Data: []string{defaultDataDir()},
	}
}

// updateReady, when set, hears of a release put in place for the next
// start, and updateAvailable of one out, to tell the user.
var (
	updateReady     func(install.Release)
	updateAvailable func(install.Release)
	// offered is the release updateAvailable told of last.
	offered install.Release
)

// updateMark starts the path of a note's action that shows the release
// told of, to take it, and whatsNewMark one that shows what the releases
// since the version after it changed.
const (
	updateMark   = "update:"
	whatsNewMark = "whatsnew:"
)

// askFirstMark is a file in the data folder that says the install was
// moved to asking first, once, so a later choice of the user's stands.
const askFirstMark = "updates-ask-first"

// restartMark is the path of a note's action that restarts into the
// release put in place.
const restartMark = "restart:"

// tellUpdates shows a card when a release is put in place for the next
// start, with Restart now, and, on the first start of one, which release
// it replaced.
func (cu *culler) tellUpdates() {
	updateAvailable = func(r install.Release) {
		cu.onDo(func() {
			offered = r
			cu.done("marraw "+r.Version+" is out", "See what changed, and update when you choose.", "See what's new", updateMark+r.Version)
		})
	}
	// Installs from before updates asked first are moved to asking, once.
	if install.IsRelease(version) {
		mark := filepath.Join(defaultDataDir(), askFirstMark)
		if _, err := os.Stat(mark); err != nil {
			app := installApp()
			if in, err := install.Find(app); err == nil && in != nil && in.Updates == install.UpdatesInstall {
				if err := install.SetUpdates(app, install.UpdatesNotify); err != nil {
					log.Printf("updates: ask first: %v", err)
				}
			}
			_ = os.WriteFile(mark, []byte("updates ask first\n"), 0o644)
		}
	}
	updateReady = func(r install.Release) {
		cu.onDo(func() {
			cu.done("marraw "+r.Version+" is ready", "It takes over the next time marraw starts.", "Restart now", restartMark)
		})
	}
	if from := install.UpdatedFrom(); from != "" {
		cu.done("Updated to marraw "+version, "From "+from+".", "What's new", whatsNewMark+from)
	}
}

// showUpdate opens the installer's window for the release told of: what
// changed since this one, and Update Now, which downloads it with its
// progress shown and restarts into it.
func (cu *culler) showUpdate() {
	r := offered
	if r.Version == "" || cu.viewer.app == nil {
		return
	}
	go func() {
		// Update Now ends this copy once the new one has started, as
		// Restart now does.
		quit := func() error {
			cu.onDo(func() { cu.c.Leave() })
			return nil
		}
		if err := install.ShowUpdate(cu.ctx, cu.viewer.app, installApp(), install.Update{Release: r, Quit: quit}); err != nil {
			cu.onDo(func() { cu.fail("The update could not be shown", err) })
		}
	}()
}

// showWhatsNew opens the installer's window on what the releases since
// from changed.
func (cu *culler) showWhatsNew(from string) {
	if cu.viewer.app == nil {
		return
	}
	go func() {
		if err := install.ShowWhatsNew(cu.ctx, cu.viewer.app, installApp(), from); err != nil {
			cu.onDo(func() { cu.fail("What's new could not be shown", err) })
		}
	}()
}

// The ways to take newer releases the settings offer, in their order.
var updateModes = []install.UpdateMode{install.UpdatesInstall, install.UpdatesNotify, install.UpdatesOff}

// setUpdates sets how the installed marraw takes newer releases.
func (cu *culler) setUpdates(mode string) {
	cu.settings.Updates = mode
	if err := install.SetUpdates(installApp(), install.UpdateMode(mode)); err != nil {
		cu.fail("Updates could not be set", err)
	}
}

// checkUpdates looks for a newer release now, and opens the installer's
// window on one, as the settings' Check now asks.
func (cu *culler) checkUpdates() {
	cu.settings.UpdateNote = "Checking…"
	cu.settingsChanged()
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, time.Minute)
		defer cancel()
		r, newer, err := install.Check(ctx, installApp())
		cu.onDo(func() {
			switch {
			case err != nil:
				cu.settings.UpdateNote = "Couldn't check: " + err.Error()
			case !newer:
				cu.settings.UpdateNote = "marraw " + version + " is up to date."
			default:
				cu.settings.UpdateNote = "marraw " + r.Version + " is out: its window shows what changed."
				offered = r
				cu.showUpdate()
			}
			cu.settingsChanged()
		})
	}()
}

// updatesState is how the installed marraw takes newer releases, for the
// settings, or nothing where this copy is not an installed release.
func updatesState() (mode string, known bool) {
	if !install.IsRelease(version) {
		return "", false
	}
	in, err := install.Find(installApp())
	if err != nil || in == nil {
		return "", false
	}
	return string(in.Updates), true
}

// restartIntoUpdate starts the release put in place and ends this copy.
func (cu *culler) restartIntoUpdate() {
	if err := install.Restart(installApp()); err != nil {
		cu.fail("marraw could not restart", err)
		return
	}
	cu.c.Leave()
}

// defaultDataDir is where a release build keeps its catalog and caches:
// a folder of its own, so it never shares the Electron app's catalog.
func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "marraw-gunim")
}

// gunimReleases are this program's releases on GitHub: those tagged
// gunim-v…, pre-releases included, as the test builds are. The version
// is the tag without its prefix.
type gunimReleases struct{ Repo string }

// ghRel is a release as GitHub's API tells of it.
type ghRel struct {
	Tag    string `json:"tag_name"`
	Page   string `json:"html_url"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// releases are the repository's published gunim releases, newest first
// as GitHub lists them.
func (g gunimReleases) releases(ctx context.Context) ([]ghRel, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+g.Repo+"/releases?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "marraw-gunim")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask GitHub for releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var all []ghRel
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&all); err != nil {
		return nil, fmt.Errorf("read what GitHub answered: %w", err)
	}
	return slices.DeleteFunc(all, func(r ghRel) bool {
		return r.Draft || !strings.HasPrefix(r.Tag, releaseTagPrefix) || !install.IsRelease(strings.TrimPrefix(r.Tag, releaseTagPrefix))
	}), nil
}

// assetName is the program's file in the release of version, for this
// system.
func assetName(version string) string {
	name := "marraw-gunim_" + strings.TrimPrefix(version, "v") + "_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// Latest implements [install.Source].
func (g gunimReleases) Latest(ctx context.Context) (install.Release, error) {
	all, err := g.releases(ctx)
	if err != nil {
		return install.Release{}, err
	}
	var best install.Release
	for _, r := range all {
		v := strings.TrimPrefix(r.Tag, releaseTagPrefix)
		if best.Version != "" && !install.Newer(v, best.Version) {
			continue
		}
		assets := map[string]string{}
		for _, a := range r.Assets {
			assets[a.Name] = a.URL
		}
		prog, ok := assets[assetName(v)]
		sums, ok2 := assets["SHA256SUMS"]
		if !ok || !ok2 {
			continue
		}
		best = install.Release{Version: v, Page: r.Page, Notes: r.Body, Name: assetName(v), Program: prog, Sums: sums,
			Signature: assets["SHA256SUMS.sig"]}
	}
	if best.Version == "" {
		return install.Release{}, fmt.Errorf("%s has no gunim release for %s/%s", g.Repo, runtime.GOOS, runtime.GOARCH)
	}
	return best, nil
}

// ReleaseNotes implements [install.Changelog].
func (g gunimReleases) ReleaseNotes(ctx context.Context) ([]install.ReleaseNotes, error) {
	all, err := g.releases(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]install.ReleaseNotes, 0, len(all))
	for _, r := range all {
		out = append(out, install.ReleaseNotes{Version: strings.TrimPrefix(r.Tag, releaseTagPrefix), Page: r.Page, Notes: r.Body})
	}
	return out, nil
}
