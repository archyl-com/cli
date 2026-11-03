package codeowners

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPattern(
	t *testing.T,
) {
	testCases := []struct {
		folder string
		want   string
	}{
		{folder: "services/orders", want: "/services/orders/"},
		{folder: "My Folder/sub dir", want: `/My\ Folder/sub\ dir/`},
		{folder: "#private", want: `/\#private/`},
		{folder: "a*b/[x]/c?/!d", want: `/a\*b/\[x\]/c\?/\!d/`},
		{folder: `back\slash`, want: `/back\\slash/`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.folder, func(t *testing.T) {
			assert.Equal(t, testCase.want, Pattern(testCase.folder))
		})
	}
}

func TestBlock(
	t *testing.T,
) {
	entries := []Entry{
		{
			Pattern:  "/services/orders/",
			Elements: []ElementRef{{Name: "Orders API", Level: "container"}, {Name: "Orders Worker", Level: "container"}},
			Owners:   []string{"@acme/checkout", "ada@example.com"},
		},
		{
			Pattern:       "/web/",
			Elements:      []ElementRef{{Name: "Storefront", Level: "container"}},
			Owners:        []string{"@acme/platform"},
			InheritedFrom: "Shop",
		},
	}
	want := BeginMarker + "\n" +
		"# Orders API (container), Orders Worker (container)\n" +
		"/services/orders/ @acme/checkout ada@example.com\n" +
		"# Storefront (container), owners from Shop\n" +
		"/web/ @acme/platform\n" +
		EndMarker + "\n"
	assert.Equal(t, want, Block(entries))
	assert.Equal(t, BeginMarker+"\n"+EndMarker+"\n", Block(nil))
}

func TestSplice(
	t *testing.T,
) {
	const block = BeginMarker + "\n/a/ @x\n" + EndMarker + "\n"
	testCases := []struct {
		name    string
		content string
		want    string
		wantErr error
	}{
		{
			name:    "a new file is the block",
			content: "",
			want:    block,
		},
		{
			name:    "a new block goes above the hand-written rules, which then win",
			content: "/backend/secrets/ @acme/security\n/docs/ @acme/writers\n",
			want:    block + "\n/backend/secrets/ @acme/security\n/docs/ @acme/writers\n",
		},
		{
			name:    "the comments heading the file stay on top",
			content: "# Owners of this repository.\n# Ask #platform.\n\n# Security reviews secrets.\n/backend/secrets/ @acme/security\n",
			want: "# Owners of this repository.\n# Ask #platform.\n\n" + block +
				"\n# Security reviews secrets.\n/backend/secrets/ @acme/security\n",
		},
		{
			name:    "a comment right above the first rule stays with it",
			content: "# Security reviews secrets.\n/backend/secrets/ @acme/security\n",
			want:    block + "\n# Security reviews secrets.\n/backend/secrets/ @acme/security\n",
		},
		{
			name:    "GitLab sections come after the block",
			content: "# Owners\n\n[Security] @acme/security\n/backend/secrets/\n",
			want:    "# Owners\n\n" + block + "\n[Security] @acme/security\n/backend/secrets/\n",
		},
		{
			name:    "a file of comments only gets the block at its end",
			content: "# Owners\n\n\n",
			want:    "# Owners\n\n" + block,
		},
		{
			name:    "the block is replaced in place",
			content: "* @acme/leads\n" + BeginMarker + "\n/old/ @y\n" + EndMarker + "\n/docs/ @acme/writers\n",
			want:    "* @acme/leads\n" + block + "/docs/ @acme/writers\n",
		},
		{
			name:    "an edited BEGIN line is still found",
			content: "# BEGIN archyl codeowners (edited)\n/old/ @y\n" + EndMarker + "\n",
			want:    block,
		},
		{
			name:    "an END line without a newline",
			content: "* @acme/leads\n" + BeginMarker + "\n/old/ @y\n" + EndMarker,
			want:    "* @acme/leads\n" + block,
		},
		{
			name:    "CRLF files stay CRLF",
			content: "* @acme/leads\r\n" + BeginMarker + "\r\n/old/ @y\r\n" + EndMarker + "\r\n",
			want:    "* @acme/leads\r\n" + BeginMarker + "\r\n/a/ @x\r\n" + EndMarker + "\r\n",
		},
		{
			name:    "a new block in a CRLF file",
			content: "/docs/ @acme/writers\r\n",
			want:    BeginMarker + "\r\n/a/ @x\r\n" + EndMarker + "\r\n\r\n/docs/ @acme/writers\r\n",
		},
		{
			name:    "an unterminated block is refused",
			content: "* @acme/leads\n" + BeginMarker + "\n/old/ @y\n",
			wantErr: ErrUnterminatedBlock,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Splice(testCase.content, block)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)

			again, err := Splice(got, block)
			require.NoError(t, err)
			assert.Equal(t, got, again, "splicing is idempotent")
		})
	}
}

func TestLocate(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		existing   []string
		gitlab     bool
		want       string
		wantExists bool
	}{
		{name: "none on GitHub", want: ".github/CODEOWNERS"},
		{name: "none on GitLab", gitlab: true, want: ".gitlab/CODEOWNERS"},
		{name: "at the root", existing: []string{"CODEOWNERS"}, want: "CODEOWNERS", wantExists: true},
		{
			name:       ".github wins over the root",
			existing:   []string{"CODEOWNERS", ".github/CODEOWNERS"},
			want:       ".github/CODEOWNERS",
			wantExists: true,
		},
		{
			name:       "docs before .gitlab",
			existing:   []string{".gitlab/CODEOWNERS", "docs/CODEOWNERS"},
			gitlab:     true,
			want:       "docs/CODEOWNERS",
			wantExists: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range testCase.existing {
				path := filepath.Join(root, filepath.FromSlash(f))
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte("* @x\n"), 0o644))
			}
			got, exists := Locate(root, testCase.gitlab)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantExists, exists)
		})
	}
}

func TestSpliceEmptyBlockRemoves(
	t *testing.T,
) {
	inserted, err := Splice("# Owners\n\n/docs/ @acme/writers\n", BeginMarker+"\n/a/ @x\n"+EndMarker+"\n")
	require.NoError(t, err)

	testCases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "a block it inserted", content: inserted, want: "# Owners\n\n/docs/ @acme/writers\n"},
		{name: "a block at the top", content: BeginMarker + "\n/a/ @x\n" + EndMarker + "\n\n/docs/ @y\n", want: "/docs/ @y\n"},
		{name: "a block at the end", content: "/docs/ @y\n\n" + BeginMarker + "\n/a/ @x\n" + EndMarker + "\n", want: "/docs/ @y\n"},
		{name: "no block", content: "/docs/ @y\n", want: "/docs/ @y\n"},
		{name: "an empty file", content: "", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Splice(testCase.content, "")
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestEqual(
	t *testing.T,
) {
	testCases := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "same", a: "a\nb\n", b: "a\nb\n", want: true},
		{name: "no final newline", a: "a\nb", b: "a\nb\n", want: true},
		{name: "mixed line endings", a: "a\r\nb\n", b: "a\r\nb\r\n", want: true},
		{name: "a changed line", a: "a\nb\n", b: "a\nc\n"},
		{name: "an added blank line", a: "a\n", b: "a\n\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Equal(testCase.a, testCase.b))
		})
	}
}
