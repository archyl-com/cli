package commands

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/config"
	"github.com/archyl-com/cli/internal/credentials"
	"github.com/archyl-com/cli/internal/ui"
)

func newLoginCmd(app *App) *cobra.Command {
	var host, appURL string
	var withToken bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Archyl",
		Long: `Sign in through your browser. The session is stored in the OS keychain and
refreshes itself; you sign in again only after a month without use.

For CI and scripts, pipe an API key instead:

  echo "$ARCHYL_API_KEY" | archyl login --with-token

or skip login entirely and set ARCHYL_API_KEY.`,
		Example: `  archyl login
  archyl login --host https://archyl.internal.example.com --profile work`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			dir, err := app.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			name, existing, _ := cfg.Resolve(app.profile)
			if name == "" {
				name = config.DefaultContext
			}
			if host == "" {
				host = firstNonEmpty(existing.Host, config.DefaultHost)
			}
			host = strings.TrimSuffix(host, "/")
			if insecureHost(host) {
				p.Eprintln(p.Bang() + " " + p.Warning("This host uses http: your tokens would cross the network unencrypted."))
			}

			var cred *credentials.Credential
			if withToken {
				key, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil && key == "" {
					return fmt.Errorf("read API key from stdin: %w", err)
				}
				cred = &credentials.Credential{APIKey: strings.TrimSpace(key)}
			} else {
				flow := &auth.Flow{
					Host: host,
					Prompt: func(link string) {
						p.Eprintln()
						p.Eprintln("  " + p.Brand() + p.Muted("  signing in to "+hostLabel(host)))
						p.Eprintln()
						p.Eprintln("  " + p.Muted("Your browser should open. If it does not, visit:"))
						p.Eprintln("  " + p.Code(link))
						p.Eprintln()
					},
				}
				if p.Interactive() {
					flow.OpenBrowser = auth.OpenBrowser
				}
				err = p.Spin("Waiting for you to approve in the browser…", func() error {
					var loginErr error
					cred, loginErr = flow.Login(ctx)
					return loginErr
				})
				if err != nil {
					return err
				}
			}

			user, err := api.NewClient(host, "", staticCreds{cred}).Me(ctx)
			if err != nil {
				return fmt.Errorf("verify credential: %w", err)
			}

			if err := credentials.NewStore(dir).Set(name, cred); err != nil {
				return fmt.Errorf("store credential: %w", err)
			}
			// A new sign-in may be someone else: never show them the last
			// one's models.
			_ = cache.ClearProfile(name)
			cfg.Contexts[name] = config.Context{
				Host:           host,
				AppURL:         firstNonEmpty(appURL, existing.AppURL),
				OrganizationID: user.DefaultOrganizationID,
				User:           user.Email,
			}
			cfg.Current = name
			if err := cfg.Save(); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(map[string]any{"profile": name, "host": host, "user": user})
			}
			if !p.Interactive() {
				p.Println(fmt.Sprintf("%s Signed in to %s as %s", p.Check(), p.Bold(hostLabel(host)), userLabel(p, user)))
				return nil
			}
			p.Println()
			p.Println(indent(p.Logo(
				p.Bold("Welcome to Archyl"),
				p.Check()+" "+p.Muted("signed in to "+hostLabel(host)+" as ")+userLabel(p, user),
				"",
				p.Hint("Next, link a repository:", "archyl init"),
			), 2))
			p.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Archyl API URL (default "+config.DefaultHost+")")
	cmd.Flags().StringVar(&appURL, "app-url", "", "web app URL when it is not the host with api. replaced by app.")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read an API key from stdin instead of opening a browser")
	return cmd
}

func newLogoutCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the stored credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := app.Session()
			if err != nil {
				return err
			}
			dir, _ := app.ConfigDir()
			if err := credentials.NewStore(dir).Delete(sess.Name); err != nil {
				return err
			}
			_ = cache.ClearProfile(sess.Name)
			app.Printer.Println(app.Printer.Check() + " Signed out of " + app.Printer.Bold(hostLabel(sess.Host)))
			return nil
		},
	}
}

func newWhoamiCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "whoami",
		Aliases: []string{"status-auth"},
		Short:   "Show who you are signed in as",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := app.Printer
			sess, err := app.Session()
			if err != nil {
				return err
			}
			cred, err := sess.Credential()
			if err != nil {
				return err
			}
			user, err := sess.Client().Me(cmd.Context())
			if err != nil {
				return err
			}

			method := "browser sign-in"
			if cred.APIKey != "" {
				method = "API key"
			}
			if p.JSON {
				return p.WriteJSON(map[string]any{
					"profile": sess.Name, "host": sess.Host, "organizationId": sess.OrganizationID,
					"user": user, "method": method,
				})
			}

			project := p.Muted("no project linked here")
			if ref, err := app.Project(cmd.Context()); err == nil {
				project = firstNonEmpty(ref.Name, ref.ID)
			}
			org := firstNonEmpty(sess.OrganizationID, user.DefaultOrganizationID)
			p.Println()
			p.Println(indent(p.Logo(
				userLabel(p, user),
				p.Muted("on ")+hostLabel(sess.Host)+p.Muted(" · profile ")+sess.Name+p.Muted(" · "+method),
				p.Muted("organization ")+org,
				p.Muted("project ")+project,
			), 2))
			p.Println()
			return nil
		},
	}
}

func newProfileCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "List and switch between Archyl instances and organizations",
		Long: `A profile is one Archyl host and organization with its own credential.
Create one by signing in with a name:

  archyl login --profile work --host https://archyl.example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := app.Printer
			dir, err := app.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(cfg)
			}
			if len(cfg.Contexts) == 0 {
				p.Println(p.Muted("No profile yet."))
				p.Println(p.Hint("Create one with", "archyl login"))
				return nil
			}
			for _, name := range cfg.Names() {
				ctx := cfg.Contexts[name]
				marker := "  "
				label := name
				if name == cfg.Current {
					marker = p.Accent("● ")
					label = p.Bold(name)
				}
				p.Println(marker + label + "  " + p.Muted(hostLabel(ctx.Host)+"  "+ctx.User))
			}
			return nil
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "use <name>",
		Short: "Make a profile the current one",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			dir, err := app.ConfigDir()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			return cfg.Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := app.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("no profile named %q", args[0])
			}
			cfg.Current = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			app.Printer.Println(app.Printer.Check() + " Now using " + app.Printer.Bold(args[0]))
			return nil
		},
	})
	return cmd
}

// staticCreds authenticates with a credential not yet stored, to verify it.
type staticCreds struct {
	cred *credentials.Credential
}

func (s staticCreds) Apply(_ context.Context, req *http.Request) error {
	if s.cred.APIKey != "" {
		req.Header.Set("X-API-Key", s.cred.APIKey)
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+s.cred.AccessToken)
	return nil
}

func (staticCreds) Invalidate(context.Context) (bool, error) { return false, nil }

func hostLabel(host string) string {
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return host
	}
	return u.Host
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// userLabel is "Ada Lovelace ada@example.com", or the email alone.
func userLabel(p *ui.Printer, user *api.User) string {
	if name := user.DisplayName(); name != "" {
		return p.Bold(name) + " " + p.Muted(user.Email)
	}
	return p.Bold(user.Email)
}

// insecureHost reports a plain-http host beyond this machine.
func insecureHost(host string) bool {
	u, err := url.Parse(host)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return false
	}
	return true
}
