package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"LICENSE", []byte("MIT")}, {name, content}} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func zipped(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	require.NoError(t, err)
	_, err = w.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// fakeRelease serves a release the way GitHub does: the latest tag on the
// API, and assets under /download/<tag>/.
func fakeRelease(t *testing.T, tag string, assets map[string][]byte) Releases {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	mux.HandleFunc("/download/"+tag+"/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := assets[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return Releases{API: server.URL + "/api", Download: server.URL + "/download"}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestUpgradeRun(
	t *testing.T,
) {
	newBinary := []byte("#!/bin/sh\necho archyl 1.2.0\n")
	errSignature := errors.New("bad signature")

	testCases := []struct {
		name      string
		goos      string
		archive   func(t *testing.T) []byte
		checksums func(archive []byte, name string) string
		verify    func(ctx context.Context, dir string) error
		wantErr   string
	}{
		{
			name:    "tar.gz release",
			goos:    "linux",
			archive: func(t *testing.T) []byte { return tarGz(t, "archyl", newBinary) },
		},
		{
			name:    "zip release on windows renames the old binary aside",
			goos:    "windows",
			archive: func(t *testing.T) []byte { return zipped(t, "archyl.exe", newBinary) },
		},
		{
			name:    "signature verified",
			goos:    "darwin",
			archive: func(t *testing.T) []byte { return tarGz(t, "archyl", newBinary) },
			verify: func(_ context.Context, dir string) error {
				for _, name := range []string{"checksums.txt", "checksums.txt.sig", "checksums.txt.pem"} {
					if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name:    "signature rejected",
			goos:    "darwin",
			archive: func(t *testing.T) []byte { return tarGz(t, "archyl", newBinary) },
			verify:  func(context.Context, string) error { return errSignature },
			wantErr: "signature does not verify",
		},
		{
			name:    "checksum mismatch",
			goos:    "linux",
			archive: func(t *testing.T) []byte { return tarGz(t, "archyl", newBinary) },
			checksums: func(_ []byte, name string) string {
				return sum([]byte("something else")) + "  " + name + "\n"
			},
			wantErr: "checksum mismatch",
		},
		{
			name:    "archive missing from checksums",
			goos:    "linux",
			archive: func(t *testing.T) []byte { return tarGz(t, "archyl", newBinary) },
			checksums: func(archive []byte, _ string) string {
				return sum(archive) + "  archyl_1.2.0_freebsd_amd64.tar.gz\n"
			},
			wantErr: "no checksum for archyl_1.2.0_linux_amd64.tar.gz",
		},
		{
			name:    "archive without the binary",
			goos:    "linux",
			archive: func(t *testing.T) []byte { return tarGz(t, "README.md", []byte("hi")) },
			wantErr: "no archyl in the archive",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			name := ArchiveName("v1.2.0", testCase.goos, "amd64")
			archive := testCase.archive(t)
			checksums := sum(archive) + "  " + name + "\n" + sum([]byte("x")) + "  other.tar.gz\n"
			if testCase.checksums != nil {
				checksums = testCase.checksums(archive, name)
			}
			releases := fakeRelease(t, "v1.2.0", map[string][]byte{
				name:                archive,
				"checksums.txt":     []byte(checksums),
				"checksums.txt.sig": []byte("sig"),
				"checksums.txt.pem": []byte("pem"),
			})

			exe := filepath.Join(t.TempDir(), "archyl")
			require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))

			tag, err := releases.Latest(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "v1.2.0", tag)

			u := Upgrade{Releases: releases, GOOS: testCase.goos, GOARCH: "amd64", Exe: exe, VerifySignature: testCase.verify}
			err = u.Run(context.Background(), tag)

			got, readErr := os.ReadFile(exe)
			require.NoError(t, readErr)
			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
				assert.Equal(t, "old", string(got), "a failed upgrade leaves the binary alone")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, newBinary, got)
			if runtime.GOOS != "windows" { // Windows has no executable bit
				info, err := os.Stat(exe)
				require.NoError(t, err)
				assert.NotZero(t, info.Mode().Perm()&0o100, "the new binary is executable")
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".archyl-upgrade-*"))
			assert.Empty(t, leftovers)
			if testCase.goos == "windows" {
				old, err := os.ReadFile(exe + ".old")
				require.NoError(t, err)
				assert.Equal(t, "old", string(old))
			}
		})
	}
}

func TestDetectMethod(
	t *testing.T,
) {
	home := filepath.FromSlash("/home/dev")
	testCases := []struct {
		name string
		exe  string
		env  map[string]string
		want Method
	}{
		{
			name: "homebrew cellar on macOS",
			exe:  "/opt/homebrew/Cellar/archyl/1.0.0/bin/archyl",
			want: MethodHomebrew,
		},
		{
			name: "linuxbrew",
			exe:  "/home/linuxbrew/.linuxbrew/Cellar/archyl/1.0.0/bin/archyl",
			want: MethodHomebrew,
		},
		{
			name: "under HOMEBREW_PREFIX",
			exe:  "/usr/local/opt/archyl/bin/archyl",
			env:  map[string]string{"HOMEBREW_PREFIX": "/usr/local/opt"},
			want: MethodHomebrew,
		},
		{
			name: "default GOPATH",
			exe:  filepath.Join(home, "go", "bin", "archyl"),
			env:  map[string]string{"HOME": home},
			want: MethodGoInstall,
		},
		{
			name: "GOBIN",
			exe:  filepath.FromSlash("/opt/gobin/archyl"),
			env:  map[string]string{"GOBIN": filepath.FromSlash("/opt/gobin")},
			want: MethodGoInstall,
		},
		{
			name: "install.sh",
			exe:  filepath.FromSlash("/usr/local/bin/archyl"),
			env:  map[string]string{"HOME": home},
			want: MethodRelease,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			getenv := func(k string) string { return testCase.env[k] }
			assert.Equal(t, testCase.want, DetectMethod(testCase.exe, getenv))
		})
	}
}

func TestArchiveName(
	t *testing.T,
) {
	assert.Equal(t, "archyl_1.2.0_darwin_arm64.tar.gz", ArchiveName("v1.2.0", "darwin", "arm64"))
	assert.Equal(t, "archyl_1.2.0_windows_amd64.zip", ArchiveName("1.2.0", "windows", "amd64"))
}
