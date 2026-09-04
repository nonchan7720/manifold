package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/nonchan7720/manifold/pkg/version"
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
	cmd.AddCommand(newOpenAPIGenerateCmd())
	return cmd
}

func newOpenAPIToolsCmd() *cobra.Command {
	var (
		serverFilter string
		toolFilter   string
		jsonOutput   bool
		fromSpec     bool
	)
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Show the MCP tools that would be generated for OpenAPI-mode servers",
		Long: "Builds the same catalog the gateway builds at startup for every " +
			"OpenAPI-mode server in the config, and prints it — without starting the gateway. " +
			"A server with tools.file configured is read from that generated file (what the " +
			"gateway would actually use); pass --from-spec to read the live spec instead.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPITools(cmd, serverFilter, toolFilter, jsonOutput, fromSpec)
		},
	}
	cmd.Flags().StringVar(&serverFilter, "server", "", "restrict to a single server name")
	cmd.Flags().StringVar(&toolFilter, "tool", "", "show full detail for tools with this name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON (includes inputSchema)")
	cmd.Flags().BoolVar(
		&fromSpec, "from-spec", false,
		"read the live spec instead of tools.file, for servers that have one configured",
	)
	return cmd
}

// runOpenAPITools drives "manifold openapi tools": it loads and builds the
// catalog for every selected OpenAPI-mode server using exactly the same
// code path as the gateway (oastomcptool.LoadSpecSource + mcpsrv.BuildCatalog),
// then prints it. A server that fails to load, or an unknown --tool, makes
// this return an error (and thus a non-zero exit) only after every other
// server has still been processed and printed.
func runOpenAPITools(
	cmd *cobra.Command, serverFilter, toolFilter string, jsonOutput, fromSpec bool,
) error {
	ctx := cmd.Context()

	names, err := selectOpenAPIServers(globalConfig, serverFilter)
	if err != nil {
		return err
	}

	catalogs, loadErr := buildCatalogs(ctx, globalConfig, names, cmd.ErrOrStderr(), fromSpec)

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

// buildCatalogs loads and builds the catalog for each of names. A server
// with tools.file configured is read from that generated file (no network
// access, mcpsrv.RegisterOpenAPI with mcpsrv.WithGeneratedToolsFile — the
// same load+build+verify path the gateway uses at startup) unless fromSpec
// is set, in which case — and for every other server — it uses the exact
// same live-spec path as the gateway: oastomcptool.LoadSpecSource followed
// by mcpsrv.BuildCatalog with a plain *http.Client (the CLI never calls
// tools, so auth transports don't matter here). Swagger 2.x specs are
// skipped with a warning (Phase 1 is OpenAPI 3.x only, per the design memo;
// a server with tools.file is never Swagger 2.x, since config validation
// requires spec for it and Init rejects a Swagger 2.x spec with tools.file).
// A server whose spec fails to load, or whose generated file is missing or
// stale, is reported on stderr and skipped, and its error is joined into the
// returned error so the caller can fail the command after every server has
// been attempted.
func buildCatalogs(
	ctx context.Context, cfg *config.Config, names []string, stderr io.Writer, fromSpec bool,
) (map[string][]mcpsrv.ToolDefinition, error) {
	catalogs := make(map[string][]mcpsrv.ToolDefinition, len(names))
	var errs []error
	for _, name := range names {
		srv := cfg.MCPServer[name]

		if file := srv.GeneratedToolsFile(); file != "" && !fromSpec {
			registry, err := mcpsrv.RegisterOpenAPI(
				ctx, srv.Spec, srv.BaseURL, srv.ExtraHeaders, mcpsrv.WithGeneratedToolsFile(file),
			)
			if err != nil {
				fmt.Fprintf(stderr, "server %q: %v\n", name, err)
				errs = append(errs, fmt.Errorf("server %q: %w", name, err))
				continue
			}
			catalogs[name] = registry.Definitions()
			continue
		}

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

// errSwagger2NotSupported is returned by loadGenerateSource for a Swagger
// 2.x spec, so runOpenAPIGenerate can tell "skip with a warning" (Phase 1
// generate is OpenAPI 3.x only, per the design memo) apart from a real
// failure.
var errSwagger2NotSupported = errors.New(
	`generate does not support Swagger 2.x specs (Phase 1 is OpenAPI 3.x only)`,
)

func newOpenAPIGenerateCmd() *cobra.Command {
	var (
		serverFilter string
		output       string
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Write the generated tools file for OpenAPI-mode servers",
		Long: "Builds the same catalog the gateway builds at startup, always from the live " +
			"spec (an existing generated file is never read as input), and writes it to each " +
			"server's tools.file — or to --output, which requires --server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPIGenerate(cmd, serverFilter, output)
		},
	}
	cmd.Flags().StringVar(&serverFilter, "server", "", "restrict to a single server name")
	cmd.Flags().StringVarP(
		&output, "output", "o", "",
		"output path (requires --server; default: the server's tools.file)",
	)
	return cmd
}

// runOpenAPIGenerate drives "manifold openapi generate": for every selected
// OpenAPI-mode server it loads the live spec (never an existing generated
// file), builds the catalog, and writes the generated tools file to
// --output (only valid with --server) or the server's tools.file. A server
// with neither is skipped with a stderr note when running over every
// server, or an error when it was named explicitly with --server. Swagger
// 2.x servers are skipped with a warning. A failure for one server is
// reported on stderr and the rest still run; the command returns a non-nil
// (joined) error at the end if any server failed or was misconfigured.
func runOpenAPIGenerate(cmd *cobra.Command, serverFilter, output string) error {
	if output != "" && serverFilter == "" {
		return fmt.Errorf("--output requires --server")
	}

	ctx := cmd.Context()
	names, err := selectOpenAPIServers(globalConfig, serverFilter)
	if err != nil {
		return err
	}

	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	var errs []error
	for _, name := range names {
		srv := globalConfig.MCPServer[name]

		outPath, skip, err := resolveGenerateOutput(srv, serverFilter, output)
		if err != nil {
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
			continue
		}
		if skip {
			fmt.Fprintf(stderr, "server %q: no tools.file configured, skipping\n", name)
			continue
		}

		n, err := generateOne(ctx, srv, outPath)
		switch {
		case errors.Is(err, errSwagger2NotSupported):
			fmt.Fprintf(stderr, "server %q: %v, skipping\n", name, err)
		case err != nil:
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
		default:
			fmt.Fprintf(stdout, "server %q: wrote %s (%d tools)\n", name, outPath, n)
		}
	}
	return errors.Join(errs...)
}

// resolveGenerateOutput picks the path a server's generated file should be
// written to: --output (only ever set together with an explicit --server,
// enforced by the caller) takes precedence, otherwise the server's
// tools.file. A server with neither is reported via skip=true (a stderr
// note, not a failure) when generate is running over every server, or as an
// error when --server named it explicitly — silently doing nothing would be
// surprising for a command invoked for exactly that one server.
func resolveGenerateOutput(
	srv *config.Server, serverFilter, output string,
) (path string, skip bool, err error) {
	if output != "" {
		return output, false, nil
	}
	if file := srv.GeneratedToolsFile(); file != "" {
		return file, false, nil
	}
	if serverFilter != "" {
		return "", false, fmt.Errorf("no tools.file configured and no --output given")
	}
	return "", true, nil
}

// loadGenerateSource fetches and loads srv.Spec (never an existing
// tools.file — generate always regenerates from the live spec), returning
// errSwagger2NotSupported for a Swagger 2.x document.
func loadGenerateSource(ctx context.Context, srv *config.Server) (*oastomcptool.SpecSource, error) {
	source, err := oastomcptool.LoadSpecSource(ctx, srv.Spec)
	if err != nil {
		return nil, err
	}
	if source.Format == oastomcptool.SpecFormatSwagger2 {
		return nil, errSwagger2NotSupported
	}
	return source, nil
}

// generateOne loads srv's live spec, builds its catalog, and writes the
// resulting generated tools file to outPath, returning the tool count on
// success.
func generateOne(ctx context.Context, srv *config.Server, outPath string) (int, error) {
	source, err := loadGenerateSource(ctx, srv)
	if err != nil {
		return 0, err
	}

	registry, err := mcpsrv.BuildCatalog(ctx, &http.Client{}, source, srv.BaseURL, srv.ExtraHeaders)
	if err != nil {
		return 0, err
	}
	defs := registry.Definitions()

	g, err := oastomcptool.NewGeneratedCatalog(
		ctx, source, mcpsrv.GeneratedTools(defs), "manifold "+version.Version, time.Now(),
	)
	if err != nil {
		return 0, err
	}

	if err := writeGeneratedFileAtomic(outPath, g); err != nil {
		return 0, err
	}
	return len(defs), nil
}

// writeGeneratedFileAtomic encodes g as the generated tools file at path: it
// creates any missing parent directories, writes to a temp file in the same
// directory (mode 0644), then renames it into place — so a failure partway
// through writing never leaves a truncated or half-written file at path.
func writeGeneratedFileAtomic(path string, g *oastomcptool.GeneratedCatalog) (rErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create output directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if rErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := oastomcptool.WriteGeneratedCatalog(tmp, g); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write generated tools file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	// Generated tools files are meant to be committed and read by anyone with
	// repo access (see docs/design/openapi-static-catalog.ja.md), so 0644 —
	// world-readable, not secret — is the correct mode here, not gosec's
	// default 0600 recommendation for arbitrary file writes.
	if err := os.Chmod(tmpPath, 0o644); err != nil { //nolint: gosec
		return fmt.Errorf("set permissions on temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file into place: %w", err)
	}
	return nil
}
