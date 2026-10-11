package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
		Data:        []string{defaultDataDir()},
	}
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
