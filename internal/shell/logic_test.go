package shell

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAction(
	t *testing.T,
) {
	testCases := []struct {
		name, reply, wantName, wantArgs string
		ok                              bool
	}{
		{name: "bare", reply: "ACTION: impact", wantName: "impact", ok: true},
		{name: "with arguments", reply: "ACTION: brief internal/refund.go internal/ledger.go\n", wantName: "brief", wantArgs: "internal/refund.go internal/ledger.go", ok: true},
		{name: "in backticks", reply: "`ACTION: check`", wantName: "check", ok: true},
		{name: "in a fence", reply: "```\nACTION: show Payments\n```", wantName: "show", wantArgs: "Payments", ok: true},
		{name: "prose around it is an answer", reply: "Let me check.\nACTION: check", ok: false},
		{name: "plain answer", reply: "Payments is owned by @payments.", ok: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			name, args, ok := ParseAction(testCase.reply)
			assert.Equal(t, testCase.ok, ok)
			assert.Equal(t, testCase.wantName, name)
			assert.Equal(t, testCase.wantArgs, args)
		})
	}
}

func TestMaybeAction(
	t *testing.T,
) {
	for _, partial := range []string{"", "A", "ACT", "ACTION:", "ACTION: imp", "  ACTION"} {
		assert.True(t, MaybeAction(partial), partial)
	}
	for _, partial := range []string{"Pay", "The branch", "AC/DC"} {
		assert.False(t, MaybeAction(partial), partial)
	}
}

func TestParseMentions(
	t *testing.T,
) {
	m := ParseMentions(`what breaks if I change @internal/refund.go, or #Payments and #"Checkout Web"? mail me@x.io`)
	assert.Equal(t, []string{"internal/refund.go"}, m.Files)
	assert.Equal(t, []string{"Payments", "Checkout Web"}, m.Elements)
}

func TestObservationAndPreview(
	t *testing.T,
) {
	obs := Observation("check", "\x1b[31mCRITICAL\x1b[0m layering", errors.New("exit 1"))
	assert.Equal(t, "Output of `archyl check` (exited with an error):\n```\nCRITICAL layering\n```", obs)
	assert.Contains(t, Observation("x", strings.Repeat("a", maxObservation+10), nil), "(truncated)")

	shown, hidden := Preview("\n\nl1\nl2\nl3\nl4\n", 2)
	assert.Equal(t, "l1\nl2", shown)
	assert.Equal(t, 2, hidden)
}

func TestActionsExposeOnlyAICommands(
	t *testing.T,
) {
	names := map[string]bool{}
	for _, a := range Actions() {
		names[a.Name] = true
		assert.NotContains(t, a.Description, "\n")
	}
	assert.True(t, names["impact"])
	assert.False(t, names["map"], "full-screen views are not offered to the assistant")
	assert.LessOrEqual(t, len(names), 30, "the API accepts at most 30 actions")
}
