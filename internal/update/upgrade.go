package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/buildinfo"
)

// Method is how this archyl was installed, which decides how it upgrades.
type Method string

const (
	// MethodRelease is a release archive or install.sh: archyl replaces itself.
	MethodRelease Method = "release"
	// MethodHomebrew upgrades through brew.
	MethodHomebrew Method = "homebrew"
	// MethodGoInstall upgrades through go install.
	MethodGoInstall Method = "go-install"
)

// GoInstallCommand reinstalls archyl from source.
const GoInstallCommand = "go install github.com/archyl-com/cli/cmd/archyl@latest"

// BrewCommand upgrades a Homebrew install.
var BrewCommand = []string{"brew", "upgrade", "archyl"}

const (
	maxChecksums = 1 << 20
	maxArchive   = 256 << 20
	maxBinary    = 256 << 20
)

// DetectMethod tells how the binary at exe, symlinks resolved, was
// installed. getenv reads HOMEBREW_PREFIX, GOBIN, GOPATH and HOME.
func DetectMethod(exe string, getenv func(string) string) Method {
	exe = filepath.ToSlash(exe)
	if strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/linuxbrew/") {
		return MethodHomebrew
	}
	if prefix := getenv("HOMEBREW_PREFIX"); prefix != "" && strings.HasPrefix(exe, filepath.ToSlash(prefix)+"/") {
		return MethodHomebrew
	}
	dir := filepath.Dir(filepath.FromSlash(exe))
	for _, bin := range goBins(getenv) {
		if filepath.Clean(bin) == dir {
			return MethodGoInstall
		}
	}
	return MethodRelease
}

func goBins(getenv func(string) string) []string {
	if gobin := getenv("GOBIN"); gobin != "" {
		return []string{gobin}
	}
	var bins []string
	for _, path := range filepath.SplitList(getenv("GOPATH")) {
		if path != "" {
			bins = append(bins, filepath.Join(path, "bin"))
		}
	}
	if len(bins) == 0 {
		if home := firstEnv(getenv, "HOME", "USERPROFILE"); home != "" {
			bins = append(bins, filepath.Join(home, "go", "bin"))
		}
	}
	return bins
}

func firstEnv(getenv func(string) string, names ...string) string {
	for _, name := range names {
		if v := getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// Releases reads archyl's GitHub releases. The zero value reads
// github.com/archyl-com/cli.
type Releases struct {
	// API is the repository's API root.
	API string
	// Download is the root release assets are served under, by tag.
	Download string
	Client   *http.Client
}

func (r Releases) api() string {
	if r.API != "" {
		return strings.TrimSuffix(r.API, "/")
	}
	return "https://api.github.com/repos/archyl-com/cli"
}

func (r Releases) download() string {
	if r.Download != "" {
		return strings.TrimSuffix(r.Download, "/")
	}
	return "https://github.com/archyl-com/cli/releases/download"
}

func (r Releases) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Latest returns the latest release's tag, "" when there is none.
func (r Releases) Latest(ctx context.Context) (string, error) {
	body, err := r.get(ctx, r.api()+"/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("read the latest release: %w", err)
	}
	return release.TagName, nil
}

func (r Releases) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	if strings.Contains(url, "/releases/latest") {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{url: url, status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", url, limit)
	}
	return body, nil
}

type statusError struct{ url, status string }

func (e *statusError) Error() string { return "GET " + e.url + ": " + e.status }

// ArchiveName is the release archive for a platform, as .goreleaser.yaml
// names it.
func ArchiveName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("archyl_%s_%s_%s%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

// Upgrade replaces a release install with another release.
type Upgrade struct {
	Releases Releases
	GOOS     string
	GOARCH   string
	// Exe is the binary to replace, symlinks resolved.
	Exe string
	// VerifySignature checks checksums.txt against its cosign signature and
	// certificate, all in dir. Nil skips the check.
	VerifySignature func(ctx context.Context, dir string) error
}

// Run downloads tag, checks it against the release's checksums and
// replaces Exe with it.
func (u Upgrade) Run(ctx context.Context, tag string) error {
	base := u.Releases.download() + "/" + tag
	archive := ArchiveName(tag, u.GOOS, u.GOARCH)

	sums, err := u.Releases.get(ctx, base+"/checksums.txt", maxChecksums)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	if u.VerifySignature != nil {
		if err := u.verifySignature(ctx, base, sums); err != nil {
			return err
		}
	}
	want, err := checksumFor(sums, archive)
	if err != nil {
		return err
	}

	data, err := u.Releases.get(ctx, base+"/"+archive, maxArchive)
	if err != nil {
		return fmt.Errorf("download %s: %w", archive, err)
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: the download is not the published archive", archive)
	}

	binary, err := extractBinary(data, u.GOOS)
	if err != nil {
		return fmt.Errorf("read %s: %w", archive, err)
	}
	return Replace(u.Exe, binary, u.GOOS)
}

func (u Upgrade) verifySignature(ctx context.Context, base string, sums []byte) error {
	dir, err := os.MkdirTemp("", "archyl-upgrade-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	files := map[string][]byte{"checksums.txt": sums}
	for _, name := range []string{"checksums.txt.sig", "checksums.txt.pem"} {
		body, err := u.Releases.get(ctx, base+"/"+name, maxChecksums)
		if err != nil {
			return fmt.Errorf("download %s: %w", name, err)
		}
		files[name] = body
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			return err
		}
	}
	if err := u.VerifySignature(ctx, dir); err != nil {
		return fmt.Errorf("the release signature does not verify: %w", err)
	}
	return nil
}

// Cosign verifies checksums.txt with the cosign binary at path, keyless,
// as SECURITY.md describes.
func Cosign(path string) func(ctx context.Context, dir string) error {
	return func(ctx context.Context, dir string) error {
		cmd := exec.CommandContext(ctx, path, "verify-blob",
			"--certificate", filepath.Join(dir, "checksums.txt.pem"),
			"--signature", filepath.Join(dir, "checksums.txt.sig"),
			"--certificate-identity-regexp", `^https://github\.com/archyl-com/cli/\.github/workflows/release\.yml@refs/tags/v`,
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
			filepath.Join(dir, "checksums.txt"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

func checksumFor(sums []byte, archive string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == archive {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in the release", archive)
}

func extractBinary(archive []byte, goos string) ([]byte, error) {
	name := "archyl"
	if goos == "windows" {
		name = "archyl.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return readLimited(rc)
		}
		return nil, fmt.Errorf("no %s in the archive", name)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s in the archive", name)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == name && hdr.Typeflag == tar.TypeReg {
			return readLimited(tr)
		}
	}
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBinary {
		return nil, errors.New("the binary is too large")
	}
	return data, nil
}

// Replace swaps exe for binary atomically: written beside it, then renamed
// over it. Windows cannot replace a running executable, so the old one is
// renamed aside to exe.old first.
func Replace(exe string, binary []byte, goos string) error {
	dir := filepath.Dir(exe)
	mode := os.FileMode(0o755)
	if info, err := os.Stat(exe); err == nil {
		mode = info.Mode().Perm() | 0o111
	}
	tmp, err := os.CreateTemp(dir, ".archyl-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}

	if goos != "windows" {
		return os.Rename(tmp.Name(), exe)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}
