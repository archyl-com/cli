package commands

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/agents"
	"github.com/archyl-com/cli/internal/mcpproxy"
)

func newMCPCmd(app *App) *cobra.Command {
	var profile string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve Archyl's MCP tools over stdio, signed in as you",
		Long: `Run Archyl's MCP server for a coding agent over stdio, authenticated with the
CLI's session. Sign in once with archyl login and every agent configured with
"archyl mcp" is connected: no OAuth flow per agent, no API key in a file.

  archyl setup --transport cli    configures your agents this way

The coding tool profile is used unless --tools says otherwise.`,
		Args:   cobra.NoArgs,
		Hidden: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := app.Session()
			if err != nil {
				return err
			}
			if _, err := sess.Credential(); err != nil {
				return err
			}
			endpoint := agents.MCPURL(sess.Host)
			if profile != "coding" {
				endpoint = strings.TrimSuffix(sess.Host, "/") + "/mcp"
				if profile != "" && profile != "full" {
					endpoint += "?profile=" + profile
				}
			}
			proxy := &mcpproxy.Proxy{Endpoint: endpoint, Creds: sess, Log: os.Stderr}
			return proxy.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&profile, "tools", "coding", "tool profile: coding, or full for every tool")
	return cmd
}
