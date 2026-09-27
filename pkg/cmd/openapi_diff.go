package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/oasbreaking"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/spf13/cobra"
)

// Output formats accepted by "openapi diff --format".
const (
	diffFormatText     = "text"
	diffFormatJSON     = "json"
	diffFormatMarkdown = "markdown"
)

func newOpenAPIDiffCmd() *cobra.Command {
	var (
		serverFilter string
		failOn       string
		format       string
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Report breaking changes between the generated tools file and the live spec",
		Long: "For every OpenAPI-mode server with tools.file configured, compares the spec " +
			"embedded in that committed file (base) against what the live spec produces " +
			"now (revision), using oasdiff, and reports each change with its severity " +
			"(error / warning = breaking, info = non-breaking) and the MCP tool it affects. " +
			"Exits non-zero if any change is at or above --fail-on, for CI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPIDiff(cmd, serverFilter, failOn, format)
		},
	}
	cmd.Flags().StringVar(&serverFilter, "server", "", "restrict to a single server name")
	cmd.Flags().StringVar(
		&failOn, "fail-on", "ERR",
		`exit non-zero if any change is at or above this level: ERR, WARN, INFO, or "" / NONE `+
			"to never fail on changes",
	)
	cmd.Flags().StringVar(
		&format, "format", diffFormatText, "output format: text, json or markdown",
	)
	return cmd
}

// serverDiff is one server's result in "openapi diff".
type serverDiff struct {
	Name    string
	Changes []oasbreaking.Change
}

// runOpenAPIDiff reports each selected server's changes between its
// generated tools file and its live spec, continuing past per-server
// failures and returning them joined, plus a failure if any change reached
// failOn.
func runOpenAPIDiff(cmd *cobra.Command, serverFilter, failOn, format string) error {
	threshold, err := oasbreaking.ParseLevel(failOn)
	if err != nil {
		return fmt.Errorf("--fail-on: %w", err)
	}
	switch format {
	case diffFormatText, diffFormatJSON, diffFormatMarkdown:
	default:
		return fmt.Errorf("--format: unknown format %q (want text, json or markdown)", format)
	}

	ctx := cmd.Context()
	names, err := selectOpenAPIServers(globalConfig, serverFilter)
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	var (
		errs    []error
		results []serverDiff
	)
	for _, name := range names {
		srv := globalConfig.MCPServer[name]

		file := srv.GeneratedToolsFile()
		if file == "" {
			fmt.Fprintf(stderr, "server %q: no tools.file configured, skipping\n", name)
			continue
		}

		changes, err := diffOne(ctx, srv, file)
		switch {
		case errors.Is(err, errSwagger2NotSupported):
			fmt.Fprintf(stderr, "server %q: %v, skipping\n", name, err)
		case err != nil:
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
		default:
			results = append(results, serverDiff{Name: name, Changes: changes})
		}
	}

	out := cmd.OutOrStdout()
	switch format {
	case diffFormatJSON:
		if err := writeDiffJSON(out, results); err != nil {
			return err
		}
	case diffFormatMarkdown:
		writeDiffMarkdown(out, results)
	default:
		writeDiffText(out, results)
	}

	if threshold != oasbreaking.LevelNone {
		failed := 0
		for _, r := range results {
			if oasbreaking.MaxLevel(r.Changes) >= threshold {
				failed++
			}
		}
		if failed > 0 {
			errs = append(errs, fmt.Errorf(
				"changes at or above %s level found in %d server(s)", threshold, failed,
			))
		}
	}
	return errors.Join(errs...)
}

// diffOne compares the spec embedded in srv's generated tools file at path
// against the would-be generated catalog from srv's live spec.
func diffOne(
	ctx context.Context, srv *config.Server, path string,
) ([]oasbreaking.Change, error) {
	_, current, err := oastomcptool.LoadGeneratedSpecSource(ctx, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing (run \"manifold openapi generate\")", path)
		}
		return nil, err
	}

	next, err := buildGeneratedCatalog(ctx, srv, time.Now())
	if err != nil {
		return nil, err
	}

	return breakingChanges(current, next)
}

// breakingChanges runs oasdiff over the embedded specs of current (base)
// and next (revision), naming the affected MCP tool on each change.
func breakingChanges(
	current, next *oastomcptool.GeneratedCatalog,
) ([]oasbreaking.Change, error) {
	base, err := oastomcptool.LoadGeneratedSpec(current)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	revision, err := oastomcptool.LoadGeneratedSpec(next)
	if err != nil {
		return nil, fmt.Errorf("revision: %w", err)
	}

	changes, err := oasbreaking.Check(base, revision)
	if err != nil {
		return nil, err
	}

	// Removed operations only exist in current; for everything else the
	// name the tool will have after regenerating wins.
	toolByOperation := make(map[string]string, len(current.Tools)+len(next.Tools))
	for _, t := range current.Tools {
		toolByOperation[t.Operation] = t.Name
	}
	for _, t := range next.Tools {
		toolByOperation[t.Operation] = t.Name
	}
	oasbreaking.ResolveTools(changes, toolByOperation)
	return changes, nil
}

// summarizeChanges returns a one-line tally such as
// "2 breaking changes (1 error, 1 warning), 1 non-breaking".
func summarizeChanges(changes []oasbreaking.Change) string {
	c := oasbreaking.Count(changes)
	if len(changes) == 0 {
		return "no API changes"
	}
	var s string
	if c.Breaking() == 0 {
		s = "no breaking changes"
	} else {
		s = fmt.Sprintf(
			"%s (%s, %s)",
			plural(c.Breaking(), "breaking change"), plural(c.Err, "error"),
			plural(c.Warn, "warning"),
		)
	}
	if c.Info > 0 {
		s += fmt.Sprintf(", %d non-breaking", c.Info)
	}
	return s
}

// plural returns "1 noun" or "n nouns".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// changeLocation renders a change's operation and tool as
// "GET /pet/{petId} (getpetbyid)", or "-" for a change outside paths.
func changeLocation(c oasbreaking.Change) string {
	switch {
	case c.Operation == "":
		return "-"
	case c.Tool == "":
		return c.Operation
	default:
		return fmt.Sprintf("%s (%s)", c.Operation, c.Tool)
	}
}

// writeChangeLine prints one change as an indented text line.
func writeChangeLine(w io.Writer, indent string, c oasbreaking.Change) {
	fmt.Fprintf(w, "%s%-8s %s  %s: %s\n", indent, c.Level, changeLocation(c), c.ID, c.Message)
}

// writeDiffText prints a summary line per server followed by one line per
// change, most severe first.
func writeDiffText(w io.Writer, results []serverDiff) {
	for _, r := range results {
		fmt.Fprintf(w, "%s: %s\n", r.Name, summarizeChanges(r.Changes))
		for _, c := range r.Changes {
			writeChangeLine(w, "  ", c)
		}
	}
}

// diffJSONEntry is one server in the --format json output.
type diffJSONEntry struct {
	Breaking int                  `json:"breaking"`
	Errors   int                  `json:"errors"`
	Warnings int                  `json:"warnings"`
	Infos    int                  `json:"infos"`
	Changes  []oasbreaking.Change `json:"changes"`
}

// writeDiffJSON writes results as a JSON object keyed by server name.
func writeDiffJSON(w io.Writer, results []serverDiff) error {
	out := make(map[string]diffJSONEntry, len(results))
	for _, r := range results {
		c := oasbreaking.Count(r.Changes)
		changes := r.Changes
		if changes == nil {
			changes = []oasbreaking.Change{}
		}
		out[r.Name] = diffJSONEntry{
			Breaking: c.Breaking(),
			Errors:   c.Err,
			Warnings: c.Warn,
			Infos:    c.Info,
			Changes:  changes,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// oasdiff のメッセージには "->" のような記号が含まれるため、HTML エスケープは行わない
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// writeDiffMarkdown prints a section with a change table per server,
// suitable for a PR comment or a GitHub Actions job summary.
func writeDiffMarkdown(w io.Writer, results []serverDiff) {
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "### %s\n\n", r.Name)
		fmt.Fprintf(w, "%s\n", summarizeChanges(r.Changes))
		if len(r.Changes) == 0 {
			continue
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Level | Operation | Tool | Rule | Message |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, c := range r.Changes {
			fmt.Fprintf(
				w, "| %s | %s | %s | `%s` | %s |\n",
				c.Level, markdownCode(c.Operation), markdownCode(c.Tool), c.ID,
				markdownCell(c.Message),
			)
		}
	}
}

// markdownCode wraps s in backticks, or returns "-" if s is empty.
func markdownCode(s string) string {
	if s == "" {
		return "-"
	}
	return "`" + s + "`"
}

// markdownCell escapes s for use inside a markdown table cell.
func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(s), " ")
}
