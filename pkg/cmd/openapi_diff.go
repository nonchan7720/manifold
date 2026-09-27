package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
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
		byTool       bool
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Report breaking changes between the generated tools file and the live spec",
		Long: "For every OpenAPI-mode server with tools.file configured, compares the spec " +
			"embedded in that committed file (base) against what the live spec produces " +
			"now (revision), using oasdiff, and reports each change with its severity " +
			"(error / warning = breaking, info = non-breaking) and the MCP tool it affects. " +
			"Exits non-zero if any change is at or above --fail-on, for CI. " +
			"With --by-tool, groups the changes by MCP tool instead, showing which " +
			"tool calls break.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPIDiff(cmd, serverFilter, failOn, format, byTool)
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
	cmd.Flags().BoolVar(
		&byTool, "by-tool", false,
		"group changes by affected MCP tool (removed / added / changed), omitting unaffected tools",
	)
	return cmd
}

// serverDiff is one server's result in "openapi diff".
type serverDiff struct {
	Name    string
	Changes []oasbreaking.Change
	// Catalog is how the generated tools differ between the committed file
	// and what the live spec produces, for --by-tool.
	Catalog oasbreaking.CatalogDiff
	// ToolCount is the number of distinct tool names on either side.
	ToolCount int
}

// failLevel is the level --fail-on is compared against: the highest change
// level, or with byTool the highest tool level, where a removed tool counts
// as an error.
func (r serverDiff) failLevel(byTool bool) oasbreaking.Level {
	if byTool {
		return oasbreaking.GroupByTool(r.Changes, r.Catalog).MaxLevel()
	}
	return oasbreaking.MaxLevel(r.Changes)
}

// runOpenAPIDiff reports each selected server's changes between its
// generated tools file and its live spec, continuing past per-server
// failures and returning them joined, plus a failure if any change reached
// failOn.
func runOpenAPIDiff(cmd *cobra.Command, serverFilter, failOn, format string, byTool bool) error {
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

		result, err := diffOne(ctx, srv, file)
		switch {
		case errors.Is(err, errSwagger2NotSupported):
			fmt.Fprintf(stderr, "server %q: %v, skipping\n", name, err)
		case err != nil:
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
		default:
			result.Name = name
			results = append(results, result)
		}
	}

	if err := writeDiff(cmd.OutOrStdout(), results, format, byTool); err != nil {
		return err
	}

	if threshold != oasbreaking.LevelNone {
		failed := 0
		for _, r := range results {
			if r.failLevel(byTool) >= threshold {
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

// writeDiff prints results in format, grouped by tool if byTool.
func writeDiff(w io.Writer, results []serverDiff, format string, byTool bool) error {
	switch {
	case format == diffFormatJSON && byTool:
		return writeDiffByToolJSON(w, results)
	case format == diffFormatJSON:
		return writeDiffJSON(w, results)
	case format == diffFormatMarkdown && byTool:
		writeDiffByToolMarkdown(w, results)
	case format == diffFormatMarkdown:
		writeDiffMarkdown(w, results)
	case byTool:
		writeDiffByToolText(w, results)
	default:
		writeDiffText(w, results)
	}
	return nil
}

// diffOne compares the spec embedded in srv's generated tools file at path
// against the would-be generated catalog from srv's live spec. The returned
// serverDiff has no Name.
func diffOne(ctx context.Context, srv *config.Server, path string) (serverDiff, error) {
	_, current, err := oastomcptool.LoadGeneratedSpecSource(ctx, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return serverDiff{}, fmt.Errorf(
				"%s is missing (run \"manifold openapi generate\")", path,
			)
		}
		return serverDiff{}, err
	}

	next, err := buildGeneratedCatalog(ctx, srv, time.Now())
	if err != nil {
		return serverDiff{}, err
	}

	changes, err := breakingChanges(current, next)
	if err != nil {
		return serverDiff{}, err
	}
	catalog, toolCount := catalogDiff(current.Tools, next.Tools)
	return serverDiff{Changes: changes, Catalog: catalog, ToolCount: toolCount}, nil
}

// catalogDiff converts mcpsrv.DiffGeneratedTools' result between current and
// next into an oasbreaking.CatalogDiff, keeping only inputSchema changes (a
// changed description or operation alone doesn't break a tool call), and
// returns the number of distinct tool names across both sides.
func catalogDiff(current, next []oastomcptool.GeneratedTool) (oasbreaking.CatalogDiff, int) {
	d := mcpsrv.DiffGeneratedTools(current, next)

	var out oasbreaking.CatalogDiff
	for _, t := range d.Added {
		out.Added = append(out.Added, oasbreaking.CatalogTool{Name: t.Name, Operation: t.Operation})
	}
	for _, t := range d.Removed {
		out.Removed = append(
			out.Removed, oasbreaking.CatalogTool{Name: t.Name, Operation: t.Operation},
		)
	}
	operationByName := make(map[string]string, len(next))
	for _, t := range next {
		operationByName[t.Name] = t.Operation
	}
	for _, c := range d.Changed {
		if slices.Contains(c.Fields, "inputSchema") {
			out.SchemaChanged = append(out.SchemaChanged, oasbreaking.CatalogTool{
				Name: c.Name, Operation: operationByName[c.Name],
			})
		}
	}
	return out, len(next) + len(d.Removed)
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

// specWideLabel names the --by-tool group of changes tied to no MCP tool.
const specWideLabel = "(spec-wide)"

// summarizeToolImpacts returns r's one-line tally for --by-tool, such as
// "2 of 3 tools affected, 1 breaking change (1 error, 0 warnings)".
func summarizeToolImpacts(r serverDiff, impacts oasbreaking.ToolImpacts) string {
	return fmt.Sprintf(
		"%d of %s affected, %s",
		len(impacts.Tools), plural(r.ToolCount, "tool"), summarizeChanges(r.Changes),
	)
}

// writeDiffByToolText prints a summary line per server, then each affected
// tool (level, status, name and operation) with its changes indented below,
// and finally the spec-wide changes.
func writeDiffByToolText(w io.Writer, results []serverDiff) {
	for _, r := range results {
		impacts := oasbreaking.GroupByTool(r.Changes, r.Catalog)
		fmt.Fprintf(w, "%s: %s\n", r.Name, summarizeToolImpacts(r, impacts))
		for _, t := range impacts.Tools {
			fmt.Fprintf(w, "  %-8s %-7s  %s (%s)\n", t.Level, t.Status, t.Name, t.Operation)
			if t.Note != "" {
				fmt.Fprintf(w, "    note: %s\n", t.Note)
			}
			for _, c := range t.Changes {
				fmt.Fprintf(w, "    %-8s %s: %s\n", c.Level, c.ID, c.Message)
			}
		}
		if len(impacts.SpecWide) > 0 {
			fmt.Fprintf(w, "  %s\n", specWideLabel)
			for _, c := range impacts.SpecWide {
				writeChangeLine(w, "    ", c)
			}
		}
	}
}

// diffToolJSONChange is one change under a tool in the --by-tool json output;
// the operation and tool are the enclosing tool's.
type diffToolJSONChange struct {
	Level   oasbreaking.Level `json:"level"`
	ID      string            `json:"id"`
	Message string            `json:"message"`
}

// diffToolJSON is one affected tool in the --by-tool json output.
type diffToolJSON struct {
	Name      string                 `json:"name"`
	Operation string                 `json:"operation"`
	Status    oasbreaking.ToolStatus `json:"status"`
	Level     oasbreaking.Level      `json:"level"`
	Note      string                 `json:"note,omitempty"`
	Changes   []diffToolJSONChange   `json:"changes"`
}

// diffByToolJSONEntry is one server in the --by-tool json output.
type diffByToolJSONEntry struct {
	Affected int                  `json:"affected"`
	Total    int                  `json:"total"`
	Tools    []diffToolJSON       `json:"tools"`
	SpecWide []oasbreaking.Change `json:"specWide"`
}

// writeDiffByToolJSON writes results grouped by tool as a JSON object keyed
// by server name.
func writeDiffByToolJSON(w io.Writer, results []serverDiff) error {
	out := make(map[string]diffByToolJSONEntry, len(results))
	for _, r := range results {
		impacts := oasbreaking.GroupByTool(r.Changes, r.Catalog)
		entry := diffByToolJSONEntry{
			Affected: len(impacts.Tools),
			Total:    r.ToolCount,
			Tools:    make([]diffToolJSON, 0, len(impacts.Tools)),
			SpecWide: impacts.SpecWide,
		}
		if entry.SpecWide == nil {
			entry.SpecWide = []oasbreaking.Change{}
		}
		for _, t := range impacts.Tools {
			changes := make([]diffToolJSONChange, 0, len(t.Changes))
			for _, c := range t.Changes {
				changes = append(
					changes, diffToolJSONChange{Level: c.Level, ID: c.ID, Message: c.Message},
				)
			}
			entry.Tools = append(entry.Tools, diffToolJSON{
				Name: t.Name, Operation: t.Operation, Status: t.Status, Level: t.Level,
				Note: t.Note, Changes: changes,
			})
		}
		out[r.Name] = entry
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// writeDiffJSON と同様、oasdiff のメッセージを読みやすく保つため HTML エスケープは行わない
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// writeDiffByToolMarkdown prints a section per server with one table whose
// rows are grouped by tool: the tool, operation and status cells are filled
// only on a tool's first row, and the spec-wide changes come last.
func writeDiffByToolMarkdown(w io.Writer, results []serverDiff) {
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		impacts := oasbreaking.GroupByTool(r.Changes, r.Catalog)
		fmt.Fprintf(w, "### %s\n\n", r.Name)
		fmt.Fprintf(w, "%s\n", summarizeToolImpacts(r, impacts))
		if len(impacts.Tools) == 0 && len(impacts.SpecWide) == 0 {
			continue
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Tool | Operation | Status | Level | Rule | Message |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |")
		for _, t := range impacts.Tools {
			status := fmt.Sprintf("**%s**", t.Status)
			if t.Level != oasbreaking.LevelNone {
				status += fmt.Sprintf(" (%s)", t.Level)
			}
			head := fmt.Sprintf(
				"| %s | %s | %s ", markdownCode(t.Name), markdownCode(t.Operation), status,
			)
			if t.Note != "" {
				fmt.Fprintf(w, "%s| - | - | _%s_ |\n", head, markdownCell(t.Note))
				head = "| | | "
			}
			for _, c := range t.Changes {
				fmt.Fprintf(
					w, "%s| %s | `%s` | %s |\n", head, c.Level, c.ID, markdownCell(c.Message),
				)
				head = "| | | "
			}
		}
		head := "| _" + specWideLabel + "_ "
		for _, c := range impacts.SpecWide {
			fmt.Fprintf(
				w, "%s| %s | | %s | `%s` | %s |\n",
				head, markdownCode(c.Operation), c.Level, c.ID, markdownCell(c.Message),
			)
			head = "| "
		}
	}
}
