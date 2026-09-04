package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/spf13/cobra"
)

// maxDescriptionRunes is the truncation length for descriptions in the table
// output ("manifold openapi tools" default format).
const maxDescriptionRunes = 80

func newOpenAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "OpenAPI static tool catalog utilities",
	}
	cmd.AddCommand(newOpenAPIToolsCmd())
	return cmd
}

func newOpenAPIToolsCmd() *cobra.Command {
	var (
		serverFilter string
		toolFilter   string
		jsonOutput   bool
	)
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Show the MCP tools that would be generated for OpenAPI-mode servers",
		Long: "Builds the same catalog the gateway builds at startup for every " +
			"OpenAPI-mode server in the config, and prints it — without starting the gateway.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPITools(cmd, serverFilter, toolFilter, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&serverFilter, "server", "", "restrict to a single server name")
	cmd.Flags().StringVar(&toolFilter, "tool", "", "show full detail for tools with this name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON (includes inputSchema)")
	return cmd
}

// runOpenAPITools drives "manifold openapi tools": it loads and builds the
// catalog for every selected OpenAPI-mode server using exactly the same
// code path as the gateway (oastomcptool.LoadSpecSource + mcpsrv.BuildCatalog),
// then prints it. A server that fails to load, or an unknown --tool, makes
// this return an error (and thus a non-zero exit) only after every other
// server has still been processed and printed.
func runOpenAPITools(cmd *cobra.Command, serverFilter, toolFilter string, jsonOutput bool) error {
	ctx := cmd.Context()

	names, err := selectOpenAPIServers(globalConfig, serverFilter)
	if err != nil {
		return err
	}

	catalogs, loadErr := buildCatalogs(ctx, globalConfig, names, cmd.ErrOrStderr())

	if toolFilter != "" {
		filtered := filterByTool(catalogs, toolFilter)
		if len(filtered) == 0 {
			toolErr := fmt.Errorf("unknown tool %q", toolFilter)
			if loadErr != nil {
				return errors.Join(loadErr, toolErr)
			}
			return toolErr
		}
		catalogs = filtered
	}

	out := cmd.OutOrStdout()
	switch {
	case jsonOutput:
		if err := writeToolsJSON(out, catalogs, names); err != nil {
			return err
		}
	case toolFilter != "":
		writeToolDetail(out, catalogs, names)
	default:
		writeToolsTable(out, catalogs, names)
	}

	return loadErr
}

// selectOpenAPIServers returns the sorted names of every OpenAPI-mode
// (Spec != "") server in cfg, or just serverFilter (if non-empty and valid).
// An unknown server name, or a server that exists but is not in OpenAPI
// mode, is an error.
func selectOpenAPIServers(cfg *config.Config, serverFilter string) ([]string, error) {
	all := make([]string, 0, len(cfg.MCPServer))
	for name, srv := range cfg.MCPServer {
		if srv.Spec == "" {
			continue
		}
		all = append(all, name)
	}
	sort.Strings(all)

	if serverFilter == "" {
		return all, nil
	}
	if slices.Contains(all, serverFilter) {
		return []string{serverFilter}, nil
	}
	if _, ok := cfg.MCPServer[serverFilter]; ok {
		return nil, fmt.Errorf(
			"server %q is not configured in OpenAPI mode (no \"spec\")", serverFilter,
		)
	}
	return nil, fmt.Errorf("unknown server %q", serverFilter)
}

// buildCatalogs loads and builds the catalog for each of names, using the
// exact same path as the gateway: oastomcptool.LoadSpecSource followed by
// mcpsrv.BuildCatalog with a plain *http.Client (the CLI never calls tools,
// so auth transports don't matter here). Swagger 2.x specs are skipped with
// a warning (Phase 1 is OpenAPI 3.x only, per the design memo). A server
// whose spec fails to load is reported on stderr and skipped, and its error
// is joined into the returned error so the caller can fail the command after
// every server has been attempted.
func buildCatalogs(
	ctx context.Context, cfg *config.Config, names []string, stderr io.Writer,
) (map[string][]mcpsrv.ToolDefinition, error) {
	catalogs := make(map[string][]mcpsrv.ToolDefinition, len(names))
	var errs []error
	for _, name := range names {
		srv := cfg.MCPServer[name]

		source, err := oastomcptool.LoadSpecSource(ctx, srv.Spec)
		if err != nil {
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
			continue
		}
		if source.Format == oastomcptool.SpecFormatSwagger2 {
			fmt.Fprintf(
				stderr,
				"server %q: Swagger 2.x is not supported by \"openapi tools\", skipping\n",
				name,
			)
			continue
		}

		registry, err := mcpsrv.BuildCatalog(
			ctx, &http.Client{}, source, srv.BaseURL, srv.ExtraHeaders,
		)
		if err != nil {
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
			continue
		}
		catalogs[name] = registry.Definitions()
	}
	return catalogs, errors.Join(errs...)
}

// filterByTool returns only the entries named tool from catalogs, keyed by
// the servers that have one.
func filterByTool(
	catalogs map[string][]mcpsrv.ToolDefinition, tool string,
) map[string][]mcpsrv.ToolDefinition {
	out := make(map[string][]mcpsrv.ToolDefinition)
	for name, defs := range catalogs {
		for _, d := range defs {
			if d.Name == tool {
				out[name] = append(out[name], d)
			}
		}
	}
	return out
}

// writeToolsTable prints one line per tool as a tab-aligned table
// (SERVER / TOOL / OPERATION / DESCRIPTION), iterating servers in names
// order (already sorted) and tools in the order Definitions() returns them
// (sorted by name).
func writeToolsTable(w io.Writer, catalogs map[string][]mcpsrv.ToolDefinition, names []string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVER\tTOOL\tOPERATION\tDESCRIPTION")
	for _, name := range names {
		for _, d := range catalogs[name] {
			desc := sanitizeDescription(d.Description)
			if d.BinaryResponse {
				desc += " [binary]"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s %s\t%s\n", name, d.Name, d.Method, d.Path, desc)
		}
	}
	_ = tw.Flush()
}

// writeToolDetail prints the full detail (server, name, operation,
// description, binaryResponse, pretty-printed inputSchema) for every tool in
// catalogs, in names order. Used for --tool without --json.
func writeToolDetail(w io.Writer, catalogs map[string][]mcpsrv.ToolDefinition, names []string) {
	first := true
	for _, name := range names {
		for _, d := range catalogs[name] {
			if !first {
				fmt.Fprintln(w)
			}
			first = false
			fmt.Fprintf(w, "server: %s\n", name)
			fmt.Fprintf(w, "name: %s\n", d.Name)
			fmt.Fprintf(w, "operation: %s %s\n", d.Method, d.Path)
			fmt.Fprintf(w, "description: %s\n", d.Description)
			fmt.Fprintf(w, "binaryResponse: %t\n", d.BinaryResponse)
			fmt.Fprintln(w, "inputSchema:")
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			_ = enc.Encode(d.InputSchema)
		}
	}
}

// toolEntry is one tool in the --json output, matching the "tools" section
// of the generated-file format from the design memo (name, operation,
// description, binaryResponse, inputSchema — in that key order).
type toolEntry struct {
	Name           string         `json:"name"`
	Operation      string         `json:"operation"`
	Description    string         `json:"description"`
	BinaryResponse bool           `json:"binaryResponse"`
	InputSchema    map[string]any `json:"inputSchema"`
}

// writeToolsJSON writes catalogs as a JSON object keyed by server name, each
// value the array of toolEntry for that server, pretty-printed with a
// trailing newline.
func writeToolsJSON(
	w io.Writer, catalogs map[string][]mcpsrv.ToolDefinition, names []string,
) error {
	out := make(map[string][]toolEntry, len(catalogs))
	for _, name := range names {
		defs, ok := catalogs[name]
		if !ok {
			continue
		}
		entries := make([]toolEntry, 0, len(defs))
		for _, d := range defs {
			entries = append(entries, toolEntry{
				Name:           d.Name,
				Operation:      fmt.Sprintf("%s %s", d.Method, d.Path),
				Description:    d.Description,
				BinaryResponse: d.BinaryResponse,
				InputSchema:    d.InputSchema,
			})
		}
		out[name] = entries
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// description には "->" のような記号が含まれるため、HTML エスケープ（\u003e）は行わない
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// sanitizeDescription collapses all whitespace (including newlines and
// tabs) to single spaces and truncates to maxDescriptionRunes with "…", so a
// multi-line or overly long OpenAPI description can't break the table.
func sanitizeDescription(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	if utf8.RuneCountInString(desc) <= maxDescriptionRunes {
		return desc
	}
	runes := []rune(desc)
	return string(runes[:maxDescriptionRunes]) + "…"
}
