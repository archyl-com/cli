package ui

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPrinter(
	t *testing.T,
) (*Printer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("ARCHYL_THEME", "dark")
	var out, errOut bytes.Buffer
	return &Printer{Out: &out, Err: &errOut}, &out, &errOut
}

func TestNewPrinter(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		json         bool
		embedded     string
		wantEmbedded bool
	}{
		{name: "plain", json: false, embedded: "", wantEmbedded: false},
		{name: "json", json: true, embedded: "", wantEmbedded: false},
		{name: "embedded in the shell", json: false, embedded: "1", wantEmbedded: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ARCHYL_EMBEDDED", testCase.embedded)
			p := NewPrinter(testCase.json)
			assert.Equal(t, testCase.json, p.JSON)
			assert.Equal(t, testCase.wantEmbedded, p.Embedded)
			assert.Same(t, os.Stdout, p.Out)
			assert.Same(t, os.Stderr, p.Err)
		})
	}
}

func TestInteractive(
	t *testing.T,
) {
	assert.False(t, (&Printer{JSON: true}).Interactive(), "--json never prompts")
	assert.False(t, (&Printer{Embedded: true}).Interactive(), "the shell's embedded commands never prompt")

	tty := term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
	assert.Equal(t, tty, (&Printer{}).Interactive())
}

func TestDetectDark(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		theme     string
		colorfgbg string
		want      bool
	}{
		{name: "ARCHYL_THEME dark", theme: "dark", colorfgbg: "0;15", want: true},
		{name: "ARCHYL_THEME light wins over COLORFGBG", theme: "light", colorfgbg: "15;0", want: false},
		{name: "ARCHYL_THEME is case-insensitive", theme: "LIGHT", want: false},
		{name: "COLORFGBG black background", colorfgbg: "15;0", want: true},
		{name: "COLORFGBG white background", colorfgbg: "0;15", want: false},
		{name: "COLORFGBG grey background is light", colorfgbg: "0;7", want: false},
		{name: "COLORFGBG dark grey background", colorfgbg: "15;8", want: true},
		{name: "COLORFGBG three fields uses the last", colorfgbg: "0;default;15", want: false},
		{name: "unknown theme falls through", theme: "solarized", colorfgbg: "0;15", want: false},
		{name: "unparseable COLORFGBG defaults dark when not interactive", colorfgbg: "default;default", want: true},
		{name: "nothing set defaults dark when not interactive", want: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ARCHYL_THEME", testCase.theme)
			t.Setenv("COLORFGBG", testCase.colorfgbg)
			assert.Equal(t, testCase.want, detectDark(false))
		})
	}
}

func TestPrinterTheme(
	t *testing.T,
) {
	t.Setenv("ARCHYL_THEME", "light")
	p := &Printer{JSON: true}
	assert.False(t, p.Dark())
	assert.Equal(t, NewTheme(false), p.Theme())

	t.Setenv("ARCHYL_THEME", "dark")
	assert.False(t, p.Dark(), "the theme is resolved once per printer")

	assert.True(t, (&Printer{JSON: true}).Dark())
	assert.Equal(t, NewTheme(true), (&Printer{JSON: true}).Theme())
	assert.NotEqual(t, NewTheme(true).Accent, NewTheme(false).Accent)
}

func TestWidth(
	t *testing.T,
) {
	if _, _, err := term.GetSize(os.Stdout.Fd()); err == nil {
		t.Skip("stdout is a terminal; its size wins over COLUMNS")
	}
	testCases := []struct {
		name    string
		columns string
		want    int
	}{
		{name: "COLUMNS", columns: "132", want: 132},
		{name: "unset", columns: "", want: 100},
		{name: "garbage", columns: "wide", want: 100},
		{name: "zero", columns: "0", want: 100},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("COLUMNS", testCase.columns)
			assert.Equal(t, testCase.want, (&Printer{}).Width())
		})
	}
}

func TestWriteJSON(
	t *testing.T,
) {
	p, out, errOut := newTestPrinter(t)
	p.JSON = true
	require.NoError(t, p.WriteJSON(map[string]any{"name": "Payments", "count": 2}))
	assert.Equal(t, "{\n  \"count\": 2,\n  \"name\": \"Payments\"\n}\n", out.String())
	assert.Empty(t, errOut.String())

	assert.Error(t, p.WriteJSON(make(chan int)))
}

func TestPrintStripsStylingWhenNotATerminal(
	t *testing.T,
) {
	p, out, errOut := newTestPrinter(t)

	p.Println(p.Success("done"), p.Bold("x"))
	p.Printf("%s=%d\n", p.Accent("n"), 3)
	p.Eprintln(p.Warning("careful"))

	assert.Equal(t, "done x\nn=3\n", out.String(), "a buffer is not a terminal: no escape codes")
	assert.Equal(t, "careful\n", errOut.String(), "status chatter goes to stderr")
}

func TestPrintHonoursNoColor(
	t *testing.T,
) {
	t.Setenv("NO_COLOR", "1")
	p, out, _ := newTestPrinter(t)
	p.Println(p.Danger("boom"))
	assert.Equal(t, "boom\n", out.String())
}

func TestComponents(
	t *testing.T,
) {
	p, _, _ := newTestPrinter(t)
	testCases := []struct {
		name string
		got  string
		want string
	}{
		{name: "brand", got: p.Brand(), want: "◆ archyl"},
		{name: "heading is trimmed and indented", got: p.Heading("  Systems \n"), want: "  Systems"},
		{name: "muted", got: p.Muted("m"), want: "m"},
		{name: "code", got: p.Code("archyl login"), want: "archyl login"},
		{name: "check", got: p.Check(), want: "✓"},
		{name: "cross", got: p.Cross(), want: "✗"},
		{name: "bang", got: p.Bang(), want: "!"},
		{name: "hint", got: p.Hint("sign in", "archyl login"), want: "→ sign in archyl login"},
		{name: "element system", got: p.Element("system", "Shop"), want: "◆ Shop"},
		{name: "element container", got: p.Element("container", "API"), want: "▣ API"},
		{name: "element component", got: p.Element("component", "Auth"), want: "◉ Auth"},
		{name: "element code", got: p.Element("code", "Login"), want: "ƒ Login"},
		{name: "element unknown", got: p.Element("other", "X"), want: "• X"},
		{name: "level", got: p.Level("container"), want: "container"},
		{name: "panel", got: p.Panel("a\nb"), want: "\n  a\n  b\n"},
		{name: "gauge healthy", got: p.Gauge(90, 10), want: strings.Repeat("━", 10) + " 90"},
		{name: "gauge clamps high", got: p.Gauge(150, 4), want: "━━━━ 100"},
		{name: "gauge clamps low", got: p.Gauge(-5, 4), want: "━━━━ 0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ansi.Strip(testCase.got))
		})
	}
}

func TestGaugeColors(
	t *testing.T,
) {
	p, _, _ := newTestPrinter(t)
	testCases := []struct {
		name  string
		score int
		want  string
	}{
		{name: "danger below 60", score: 59, want: p.Danger("59")},
		{name: "warning below 80", score: 60, want: p.Warning("60")},
		{name: "success from 80", score: 80, want: p.Success("80")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			gauge := p.Gauge(testCase.score, 10)
			number := gauge[strings.LastIndex(gauge, " ")+1:]
			assert.Equal(t, ansi.Strip(testCase.want), ansi.Strip(number))
			// Same hue as the matching status style, bolded.
			assert.Contains(t, gauge, colorCode(t, testCase.want))
		})
	}
}

// colorCode extracts the SGR color parameters a styled string opens with.
func colorCode(
	t *testing.T,
	styled string,
) string {
	t.Helper()
	start := strings.Index(styled, "38;")
	require.GreaterOrEqual(t, start, 0, "styled output carries a foreground color")
	end := strings.IndexAny(styled[start:], "m")
	return styled[start : start+end]
}

func TestKeyValues(
	t *testing.T,
) {
	p, _, _ := newTestPrinter(t)
	got := ansi.Strip(p.KeyValues([][2]string{
		{"Host", "https://api.archyl.com"},
		{"Organization", "Acme"},
	}))
	assert.Equal(t, "Host          https://api.archyl.com\nOrganization  Acme", got)
	assert.Empty(t, p.KeyValues(nil))
}

func TestPlural(
	t *testing.T,
) {
	testCases := []struct {
		n    int
		want string
	}{
		{n: 0, want: "0 files"},
		{n: 1, want: "1 file"},
		{n: 2, want: "2 files"},
		{n: -1, want: "-1 files"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.want, func(t *testing.T) {
			assert.Equal(t, testCase.want, Plural(testCase.n, "file", "files"))
		})
	}
}

func TestLevelColor(
	t *testing.T,
) {
	theme := NewTheme(true)
	testCases := []struct {
		level string
		want  any
		glyph string
	}{
		{level: "system", want: theme.System, glyph: "◆"},
		{level: "container", want: theme.Container, glyph: "▣"},
		{level: "component", want: theme.Component, glyph: "◉"},
		{level: "code", want: theme.Code, glyph: "ƒ"},
		{level: "", want: theme.Muted, glyph: "•"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.level, func(t *testing.T) {
			assert.Equal(t, testCase.want, theme.LevelColor(testCase.level))
			assert.Equal(t, testCase.glyph, LevelGlyph(testCase.level))
		})
	}
}

func TestPrinterLogo(
	t *testing.T,
) {
	p, _, _ := newTestPrinter(t)
	testCases := []struct {
		name   string
		beside []string
	}{
		{name: "no text", beside: nil},
		{name: "four lines", beside: []string{"archyl", "v1.0.0", "Acme", "prod"}},
		{name: "empty lines leave rows bare", beside: []string{"archyl", "", "v1.0.0"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(ansi.Strip(p.Logo(testCase.beside...)), "\n")
			require.Len(t, lines, 6)
			for i, line := range lines {
				mark := strings.TrimRight(logoRows[i], " ")
				require.True(t, strings.HasPrefix(line, mark), "row %d starts with the mark", i)
				text := strings.TrimSpace(strings.TrimPrefix(line, mark))
				j := i - logoTextRow
				switch {
				case j >= 0 && j < len(testCase.beside) && testCase.beside[j] != "":
					assert.Equal(t, testCase.beside[j], text, "row %d", i)
					column := strings.Index(line, text)
					require.GreaterOrEqual(t, column, 0, "row %d holds its text", i)
					assert.Equal(t, logoWidth+3, len([]rune(line[:column])), "row %d text column", i)
				default:
					assert.Empty(t, text, "row %d carries no text", i)
				}
			}
		})
	}
}

func TestLogoLightAndDarkDiffer(
	t *testing.T,
) {
	dark := NewTheme(true).layerColors(true)
	light := NewTheme(false).layerColors(false)
	require.Len(t, dark, 4)
	require.Len(t, light, 4)
	assert.NotEqual(t, dark[0], light[0], "top layer flips between white and black")
}

func TestSpinWithoutTerminal(
	t *testing.T,
) {
	p, _, errOut := newTestPrinter(t)
	p.JSON = true
	called := false
	sentinel := errors.New("boom")
	err := p.Spin("working", func() error { called = true; return sentinel })
	assert.True(t, called)
	assert.ErrorIs(t, err, sentinel)
	assert.Empty(t, errOut.String(), "no spinner frames outside a terminal")
}
