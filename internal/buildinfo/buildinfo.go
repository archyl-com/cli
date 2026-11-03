// Package buildinfo carries the version stamped into the binary at release.
package buildinfo

// Set by GoReleaser through -ldflags.
var (
	Version = "dev"
	Commit  = ""
)

// UserAgent identifies the CLI to the API.
func UserAgent() string {
	return "archyl-cli/" + Version
}
