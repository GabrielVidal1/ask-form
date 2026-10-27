// Package update finds, downloads and installs ask-form releases.
//
// Releases are GitHub releases of the ask-form repo, one raw binary per
// platform plus a checksums.txt (sha256sum format):
//
//	<repo>/releases/latest                        302 → <repo>/releases/tag/<tag>
//	<repo>/releases/download/<tag>/ask-form-<os>-<arch>
//	<repo>/releases/download/<tag>/checksums.txt
//
// The redirect gives the latest tag without the GitHub API (and its rate
// limit). A binary is installed only when its SHA-256 matches checksums.txt
// and it runs on this machine; it replaces the running one with a rename, so
// a crash midway leaves the old binary in place.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is where releases come from; ASK_FORM_UPDATE_URL overrides it
// (a fork, a mirror, a test server).
const DefaultRepo = "https://github.com/GabrielVidal1/ask-form"

// Repo is the release source in use.
func Repo() string {
	if u := os.Getenv("ASK_FORM_UPDATE_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultRepo
}

// Asset is this platform's binary name in a release.
func Asset(bin string) string {
	name := fmt.Sprintf("%s-%s-%s", bin, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// ── versions ────────────────────────────────────────────────────────────────

// Version is a semantic version, vMAJOR.MINOR.PATCH[-PRE].
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// Parse reads "v1.2.3", "1.2.3" or "v1.2.3-rc.1". A dev build ("dev", a
// commit hash) is not a version.
func Parse(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, _ := strings.Cut(s, "-")
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre}, true
}

func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Less orders versions; a pre-release sorts before its release.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	if v.Patch != o.Patch {
		return v.Patch < o.Patch
	}
	return v.Pre != "" && (o.Pre == "" || v.Pre < o.Pre)
}

// Release reports whether v is a plain release (no pre-release suffix — a
// `git describe` build like v0.1.0-3-g7fedbda counts as one).
func (v Version) Release() bool { return v.Pre == "" }

// ── fetching ────────────────────────────────────────────────────────────────

var client = &http.Client{Timeout: 2 * time.Minute}

// Latest returns the newest release's tag.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, Repo()+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ask-form-update")
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("check for a release: %w", err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("check for a release: %s answered HTTP %d (no release yet?)", Repo(), res.StatusCode)
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("check for a release: bad redirect %q", loc)
	}
	dir, tag := path.Split(u.Path)
	if !strings.HasSuffix(dir, "/tag/") || tag == "" {
		return "", fmt.Errorf("check for a release: unexpected redirect %q", loc)
	}
	tag, _ = url.PathUnescape(tag)
	if _, ok := Parse(tag); !ok {
		return "", fmt.Errorf("check for a release: latest tag %q is not a version", tag)
	}
	return tag, nil
}

func get(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ask-form-update")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", rawURL, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", rawURL, max)
	}
	return b, nil
}

// Checksums parses a sha256sum file into name → hex digest.
func Checksums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// Fetch downloads one asset of a release and checks it against the
// release's checksums.txt.
func Fetch(ctx context.Context, tag, asset string) ([]byte, error) {
	base := Repo() + "/releases/download/" + url.PathEscape(tag) + "/"
	sums, err := get(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return nil, err
	}
	want, ok := Checksums(sums)[asset]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s (no build for this platform?)", tag, asset)
	}
	bin, err := get(ctx, base+asset, 128<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s %s: checksum mismatch (got %s, checksums.txt says %s)", asset, tag, got, want)
	}
	return bin, nil
}

// ── installing ──────────────────────────────────────────────────────────────

// Executable is the running binary's real path (symlinks resolved).
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Install replaces the binary at exe with bin, after checking that bin runs
// here and reports the expected version (want; "" skips that check).
func Install(exe string, bin []byte, want string) error {
	dir := filepath.Dir(exe)
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode().Perm() | 0o100
	}
	tmp, err := os.CreateTemp(dir, ".ask-form-upgrade-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s (%v) — reinstall it, or run the upgrade as the binary's owner", exe, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once renamed
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	if err := smoke(name, want); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// A running .exe can be renamed but not replaced.
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
	}
	return os.Rename(name, exe)
}

// smoke runs `<bin> version` and checks what it reports.
func smoke(bin, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded binary doesn't run here: %v", err)
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(out, &v) != nil || v.Version == "" {
		return errors.New("the downloaded binary doesn't report a version")
	}
	if want != "" && v.Version != want {
		return fmt.Errorf("the downloaded binary reports %s, expected %s", v.Version, want)
	}
	return nil
}

// ── policy ──────────────────────────────────────────────────────────────────

// Due says whether an automatic upgrade from cur to latest should happen,
// and why not when it shouldn't. Dev builds and pre-releases never
// auto-upgrade; a new major only when majors is set.
func Due(cur, latest string, majors bool) (bool, string) {
	c, ok := Parse(cur)
	if !ok || !c.Release() {
		return false, "dev build (" + cur + ")"
	}
	l, ok := Parse(latest)
	if !ok {
		return false, "latest " + latest + " is not a version"
	}
	if !c.Less(l) {
		return false, "up to date"
	}
	if l.Major != c.Major && !majors {
		return false, "new major version " + latest + ": run `ask-form upgrade`"
	}
	return true, ""
}

// Newer reports whether latest is newer than cur (false for a dev build).
func Newer(cur, latest string) bool {
	c, ok1 := Parse(cur)
	l, ok2 := Parse(latest)
	return ok1 && ok2 && c.Less(l)
}
