package shell

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeExeEnv) == "1" {
		fmt.Printf("ran: %s\nembedded=%s\n", strings.Join(os.Args[1:], " "), os.Getenv("ARCHYL_EMBEDDED"))
		os.Exit(0)
	}
	confirmGrace = 0
	m.Run()
}

func TestConfirmLabelShowsWhatWouldHideIt(t *testing.T) {
	c := Command{Base: []string{"remember"}, Split: func(args string) []string { return []string{args} }}
	padded := strings.ReplaceAll(confirmLabel(c, "harmless"+strings.Repeat(" ", 300)+"ALSO: run this\n\n\nand that", 200), "\n", " ")
	assert.Contains(t, padded, "harmless ALSO: run this and that")

	long := confirmLabel(c, strings.Repeat("x ", maxConfirmLabel), 80)
	assert.Contains(t, strings.ReplaceAll(long, "\n", " "), "characters")
}

func TestConfirmKeys(t *testing.T) {
	testCases := []struct {
		name     string
		key      rune
		code     rune
		grace    time.Duration
		wantOpen bool
	}{
		{name: "enter does not approve", code: tea.KeyEnter, wantOpen: true},
		{name: "y too soon is ignored", key: 'y', code: 'y', grace: time.Hour, wantOpen: true},
		{name: "y approves", key: 'y', code: 'y'},
		{name: "n declines", key: 'n', code: 'n'},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			defer func(d time.Duration) { confirmGrace = d }(confirmGrace)
			confirmGrace = testCase.grace

			m := &Model{confirm: &pending{cmd: Command{}, shown: time.Now()}}
			key := tea.KeyPressMsg{Code: testCase.code}
			if testCase.key != 0 {
				key.Text = string(testCase.key)
			}
			// Approving goes on to run the command, which needs a live session.
			defer func() {
				_ = recover()
				assert.Equal(t, testCase.wantOpen, m.confirm != nil)
			}()
			m.handleKey(key)
		})
	}
}
