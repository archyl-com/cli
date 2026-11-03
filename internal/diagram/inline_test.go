package diagram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectProtocol(
	t *testing.T,
) {
	testCases := []struct {
		name string
		env  map[string]string
		want Protocol
	}{
		{name: "plain xterm", env: map[string]string{"TERM": "xterm-256color"}, want: NoImages},
		{name: "kitty", env: map[string]string{"TERM": "xterm-kitty"}, want: Kitty},
		{name: "kitty window", env: map[string]string{"KITTY_WINDOW_ID": "1"}, want: Kitty},
		{name: "ghostty", env: map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, want: Kitty},
		{name: "ghostty resources", env: map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, want: Kitty},
		{name: "iterm2", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, want: ITerm},
		{name: "wezterm", env: map[string]string{"TERM_PROGRAM": "WezTerm"}, want: ITerm},
		{name: "iterm2 through tmux", env: map[string]string{"TMUX": "/tmp/t,1,0", "TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2"}, want: ITerm},
		{name: "kitty through tmux falls back", env: map[string]string{"TMUX": "/tmp/t,1,0", "KITTY_WINDOW_ID": "1"}, want: NoImages},
		{name: "turned off", env: map[string]string{"TERM": "xterm-kitty", "ARCHYL_IMAGES": "0"}, want: NoImages},
		{name: "forced", env: map[string]string{"ARCHYL_IMAGES": "iterm2"}, want: ITerm},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := DetectProtocol(func(k string) string { return testCase.env[k] })
			assert.Equal(t, testCase.want, got, got.String())
		})
	}
}

func TestWriteInline(
	t *testing.T,
) {
	png := []byte(strings.Repeat("x", 5000))

	var kitty strings.Builder
	require.NoError(t, WriteInline(&kitty, Kitty, png, 80, false))
	out := kitty.String()
	assert.True(t, strings.HasPrefix(out, "\x1b_Ga=T,f=100,q=2,c=80,m=1;"))
	assert.Equal(t, 2, strings.Count(out, "\x1b_G"), "base64 split in 4096-byte chunks")
	assert.Contains(t, out, "\x1b_Gm=0;")

	var iterm strings.Builder
	require.NoError(t, WriteInline(&iterm, ITerm, png, 60, false))
	assert.True(t, strings.HasPrefix(iterm.String(), "\x1b]1337;File=inline=1;size=5000;width=60;"))
	assert.True(t, strings.HasSuffix(iterm.String(), "\a\n"))

	var tmux strings.Builder
	require.NoError(t, WriteInline(&tmux, ITerm, png, 60, true))
	assert.True(t, strings.HasPrefix(tmux.String(), "\x1bPtmux;\x1b\x1b]1337;"))
	assert.True(t, strings.HasSuffix(tmux.String(), "\x1b\\\n"))

	assert.Error(t, WriteInline(&strings.Builder{}, NoImages, png, 60, false))
}

func TestInlineColumns(
	t *testing.T,
) {
	assert.Equal(t, 100, InlineColumns(800, 200))
	assert.Equal(t, 79, InlineColumns(1600, 80), "never wider than the terminal")
	assert.Equal(t, 20, InlineColumns(40, 80))
}
