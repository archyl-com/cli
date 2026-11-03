package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/ui"
)

// Diagram formats.
const (
	formatAuto    = "auto"
	formatText    = "text"
	formatImage   = "image"
	formatSVG     = "svg"
	formatPNG     = "png"
	formatMermaid = "mermaid"
)

func newDiagramCmd(app *App) *cobra.Command {
	var format, output string
	var inside bool
	var depth int

	cmd := &cobra.Command{
		Use:   "diagram [element]",
		Short: "Draw a C4 diagram in the terminal",
		Long: `Draw a C4 diagram of the model.

Without an element, it draws the system context. Name an element of any level,
system, container, component or code, and the diagram is centred on it: the
element in the middle and everything its relationships reach, both ways,
within --depth hops (1 to 3). Elements deeper than it are drawn as their
ancestor at its level, and neighbours from elsewhere name their parent.

--inside draws the C4 view inside the element instead: a system's containers
or a container's components, with the elements around them dashed.

In Kitty, Ghostty, WezTerm and iTerm2 the diagram is an image; elsewhere,
and in pipes, it is drawn with box-drawing characters. Inside tmux, images
need "set -g allow-passthrough on" and an iTerm2-compatible terminal.
ARCHYL_IMAGES=0 always draws text.

Formats: text, image (inline in the terminal), png, svg, mermaid. With -o the
format follows the file's extension (.png, .svg, .mmd, .md, .txt).`,
		Example: `  archyl diagram
  archyl diagram Checkout
  archyl diagram "Order Repository" --depth 2
  archyl diagram Billing --inside
  archyl diagram "Billing API" -o billing-api.png
  archyl diagram --format mermaid > context.mmd
  archyl diagram Billing --json | jq '.nodes[].name'`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			g, project, err := loadGraph(ctx, app, ref.ID)
			if err != nil {
				return err
			}
			if len(g.Roots) == 0 {
				p := app.Printer
				p.Println(p.Muted("The model of " + project.Name + " is empty."))
				p.Println(p.Hint("Discover it from your code in the web app, or push a DSL file:", "archyl sync"))
				return nil
			}
			if len(args) == 0 && (inside || cmd.Flags().Changed("depth")) {
				return fmt.Errorf("name an element: archyl diagram <element> --inside or --depth 2")
			}
			if inside && cmd.Flags().Changed("depth") {
				return fmt.Errorf("--depth centres on the element; it does not go with --inside")
			}
			if depth < 1 || depth > diagram.MaxFocusDepth {
				return fmt.Errorf("--depth must be between 1 and %d", diagram.MaxFocusDepth)
			}
			view := diagram.For(g, nil)
			if len(args) == 1 {
				n, others := g.Find(args[0])
				if n == nil {
					return fmt.Errorf("no element matches %q; find one with archyl search %s", args[0], args[0])
				}
				if len(others) > 0 && !strings.EqualFold(n.Name, args[0]) {
					app.Printer.Eprintln(app.Printer.Muted(fmt.Sprintf("%q matches %d elements; drawing %s (%s). Name it in full for another.",
						args[0], len(others)+1, n.Name, n.Level)))
				}
				switch {
				case !inside:
					view = diagram.Focus(g, n, depth)
				case len(n.Children) == 0:
					return fmt.Errorf("%s holds no elements to draw inside it; drop --inside to centre the diagram on it", n.Name)
				default:
					view = diagram.For(g, n)
				}
			}
			return printDiagram(app, view, view.Hops(), format, output)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatAuto, "text, image, png, svg or mermaid (default: image in capable terminals, else text)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file; the extension picks the format")
	cmd.Flags().BoolVar(&inside, "inside", false, "draw what the element contains instead of centring on it")
	cmd.Flags().IntVar(&depth, "depth", 1, "hops around the element (1 to 3)")
	_ = cmd.RegisterFlagCompletionFunc("format", cobra.FixedCompletions(
		[]string{formatText, formatImage, formatPNG, formatSVG, formatMermaid}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// diagramFormat settles the format from the flag, the output file and the
// terminal.
func diagramFormat(p *ui.Printer, format, output string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case formatText, formatImage, formatPNG, formatSVG, formatMermaid:
		return format, nil
	case "", formatAuto:
	default:
		return "", fmt.Errorf("unknown format %q: use text, image, png, svg or mermaid", format)
	}
	if output != "" {
		switch strings.ToLower(filepath.Ext(output)) {
		case ".png":
			return formatPNG, nil
		case ".svg":
			return formatSVG, nil
		case ".mmd", ".mermaid", ".md":
			return formatMermaid, nil
		}
		return formatText, nil
	}
	if p.Interactive() && diagram.DetectProtocol(os.Getenv) != diagram.NoImages {
		return formatImage, nil
	}
	return formatText, nil
}

// printDiagram renders a view in the chosen format to stdout or a file.
func printDiagram(app *App, view *diagram.View, hl diagram.Highlight, format, output string) error {
	p := app.Printer
	if p.JSON {
		layout := diagram.ArrangeText(view, diagram.TextBoxMax)
		return p.WriteJSON(struct {
			Kind    string `json:"kind"`
			Title   string `json:"title"`
			ScopeID string `json:"scopeId,omitempty"`
			FocusID string `json:"focusId,omitempty"`
			Units   string `json:"units"`
			*diagram.Layout
		}{view.Kind, view.Title, view.ScopeID, view.FocusID, "cells", layout})
	}
	format, err := diagramFormat(p, format, output)
	if err != nil {
		return err
	}
	theme, dark := p.Theme(), p.Dark()
	imageOpts := diagram.ImageOptions{Theme: theme, Dark: dark, Highlight: hl}

	var body []byte
	switch format {
	case formatImage:
		proto := diagram.DetectProtocol(os.Getenv)
		if proto == diagram.NoImages {
			return fmt.Errorf("this terminal does not show images; write a file with archyl diagram -o diagram.png")
		}
		img, width, err := diagram.PNG(view, imageOpts)
		if err != nil {
			return err
		}
		if output != "" {
			body = img
			break
		}
		p.Println(diagramHeader(p, view))
		return diagram.WriteInline(p.Out, proto, img, diagram.InlineColumns(width, p.Width()), diagram.InTmux(os.Getenv))
	case formatPNG:
		img, _, err := diagram.PNG(view, imageOpts)
		if err != nil {
			return err
		}
		if output == "" && term.IsTerminal(os.Stdout.Fd()) {
			return fmt.Errorf("a PNG would garble the terminal; write it to a file with -o diagram.png")
		}
		body = img
	case formatSVG:
		var buf bytes.Buffer
		if err := diagram.SVG(&buf, view, imageOpts); err != nil {
			return err
		}
		body = buf.Bytes()
	case formatMermaid:
		text := diagram.Mermaid(view, hl)
		if strings.EqualFold(filepath.Ext(output), ".md") {
			text = "```mermaid\n" + text + "```\n"
		}
		body = []byte(text)
	default:
		if output != "" {
			body = []byte(diagram.Text(view, diagram.TextOptions{Highlight: hl}) + "\n")
			break
		}
		p.Println(diagramHeader(p, view))
		p.Println()
		p.Println(diagram.Text(view, diagram.TextOptions{Theme: theme, Color: true, Highlight: hl, Width: p.Width()}))
		if len(hl) > 0 {
			p.Println()
			p.Println(diagramLegend(p, view, hl))
		}
		return nil
	}

	if output == "" {
		_, err := p.Out.Write(body)
		return err
	}
	if err := os.WriteFile(output, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}
	p.Eprintln(p.Check() + " Wrote " + p.Bold(view.Title) + " to " + p.Code(output))
	return nil
}

func diagramHeader(p *ui.Printer, view *diagram.View) string {
	return p.Brand() + p.Muted("  diagram  ") + p.Bold(view.Title)
}

func diagramLegend(p *ui.Printer, view *diagram.View, hl diagram.Highlight) string {
	t := p.Theme()
	if view.Kind == diagram.KindFocus {
		far := 0
		for _, d := range hl {
			far = max(far, d)
		}
		legend := "  " + p.Accent("┏━┓") + p.Muted(" focus")
		for d := 1; d <= far; d++ {
			unit := " hop"
			if d > 1 {
				unit = " hops"
			}
			legend += "   " + diagram.Swatch(t, d) + p.Muted(" "+strconv.Itoa(d)+unit)
		}
		return legend
	}
	return "  " + p.Accent("┏━┓") + p.Muted(" touched   ") +
		diagram.Swatch(t, 1) + p.Muted(" depends on it   ") +
		diagram.Swatch(t, 100) + p.Muted(" unrelated")
}
