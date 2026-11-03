package wrap

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentArgs(
	t *testing.T,
) {
	testCases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "claude gets the briefing as an appended system prompt",
			args: []string{"claude", "--model", "opus"},
			want: []string{"claude", "--append-system-prompt", "B", "--model", "opus"},
		},
		{
			name: "a full path to claude is recognised",
			args: []string{"/usr/local/bin/claude"},
			want: []string{"/usr/local/bin/claude", "--append-system-prompt", "B"},
		},
		{
			name: "an explicit system prompt is respected",
			args: []string{"claude", "--append-system-prompt", "mine"},
			want: []string{"claude", "--append-system-prompt", "mine"},
		},
		{
			name: "other agents are left alone",
			args: []string{"codex", "fix it"},
			want: []string{"codex", "fix it"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, AgentArgs(testCase.args, "B", "/tmp/b.md"))
		})
	}
}

func TestAgentArgsLongBriefing(
	t *testing.T,
) {
	briefing := strings.Repeat("a guardrail to respect\n", 2000)
	args := AgentArgs([]string{"claude"}, briefing, "/tmp/archyl-session-s1.md")
	require.Len(t, args, 3)
	inline := args[2]
	assert.LessOrEqual(t, len(inline), MaxInlineBriefing)
	assert.True(t, strings.HasPrefix(inline, "a guardrail to respect\n"))
	assert.Contains(t, inline, "a guardrail to respect\n\n[The briefing goes on", "cut on a line")
	assert.Contains(t, inline, "/tmp/archyl-session-s1.md ($ARCHYL_BRIEFING_FILE)")
}

func TestExec(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}

	testCases := []struct {
		name      string
		script    string
		wantCode  int
		wantBeats bool
	}{
		{name: "exit code is passed through", script: "exit 3", wantCode: 3},
		{name: "heartbeats while the agent runs", script: "sleep 0.3", wantBeats: true},
		{name: "environment reaches the agent", script: `test "$ARCHYL_SESSION_ID" = s1`, wantCode: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			beats := 0
			code, err := Exec(context.Background(), []string{"sh", "-c", testCase.script},
				append(os.Environ(), "ARCHYL_SESSION_ID=s1"), 50*time.Millisecond,
				func(context.Context) { beats++ })
			require.NoError(t, err)
			assert.Equal(t, testCase.wantCode, code)
			if testCase.wantBeats {
				assert.Positive(t, beats)
			}
		})
	}
}
