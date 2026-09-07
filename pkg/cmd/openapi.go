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

// runOpenAPITools builds and prints the tool catalog for every selected
// server, continuing past per-server failures and returning them joined.
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

// selectOpenAPIServers returns the sorted names of every OpenAPI-mode server
// in cfg, or just serverFilter if it is set and valid.
func selectOpenAPIServers(cfg *config.Config, serverFilter string) ([]string, error) {
	all := make([]string, 0, len(cfg.MCPServer))
	for name, srv := range cfg.MCPServer {
		if !srv.IsOpenAPI() {
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
			"server %q is not configured in OpenAPI mode (no \"spec\" or \"tools.file\")",
			serverFilter,
		)
	}
	return nil, fmt.Errorf("unknown server %q", serverFilter)
}

// buildCatalogs builds each named server's catalog from tools.file, or from
// the live spec when fromSpec is set or no tools.file is configured,
// skipping Swagger 2.x specs and joining any per-server errors.
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

		if srv.Spec == "" {
			err := fmt.Errorf("--from-spec requires spec to be configured")
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
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

// filterByTool returns only the entries named tool from catalogs.
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

// writeToolsTable prints one tab-aligned table line per tool.
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

// writeToolDetail prints the full detail for every tool in catalogs.
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

// toolEntry is one tool in the --json output.
type toolEntry struct {
	Name           string         `json:"name"`
	Operation      string         `json:"operation"`
	Description    string         `json:"description"`
	BinaryResponse bool           `json:"binaryResponse"`
	InputSchema    map[string]any `json:"inputSchema"`
}

// writeToolsJSON writes catalogs as a JSON object keyed by server name.
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

// sanitizeDescription collapses whitespace to single spaces and truncates
// to maxDescriptionRunes with "…".
func sanitizeDescription(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	if utf8.RuneCountInString(desc) <= maxDescriptionRunes {
		return desc
	}
	runes := []rune(desc)
	return string(runes[:maxDescriptionRunes]) + "…"
}

// errSwagger2NotSupported marks a Swagger 2.x spec passed to generate.
var errSwagger2NotSupported = errors.New(
	`generate does not support Swagger 2.x specs (Phase 1 is OpenAPI 3.x only)`,
)

// errSpecRequiredForGenerate marks a server with no spec configured.
var errSpecRequiredForGenerate = errors.New("spec is required to generate the tools file")

func newOpenAPIGenerateCmd() *cobra.Command {
	var (
		serverFilter string
		output       string
		check        bool
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Write the generated tools file for OpenAPI-mode servers",
		Long: "Builds the same catalog the gateway builds at startup, always from the live " +
			"spec (an existing generated file is never read as input), and writes it to each " +
			"server's tools.file — or to --output, which requires --server. With --check, " +
			"nothing is written: the would-be catalog is compared against what's on disk and " +
			"any drift is reported (exit non-zero), for CI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runOpenAPIGenerateCheck(cmd, serverFilter, output)
			}
			return runOpenAPIGenerate(cmd, serverFilter, output)
		},
	}
	cmd.Flags().StringVar(&serverFilter, "server", "", "restrict to a single server name")
	cmd.Flags().StringVarP(
		&output, "output", "o", "",
		"output path (requires --server; default: the server's tools.file)",
	)
	cmd.Flags().BoolVar(
		&check, "check", false,
		"check whether the generated tools file is up to date instead of writing it "+
			"(exit non-zero on drift)",
	)
	return cmd
}

// runOpenAPIGenerate builds each selected server's catalog from its live
// spec and writes the generated tools file, continuing past per-server
// failures and returning them joined.
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

// resolveGenerateOutput picks the output path for srv: --output, else
// tools.file, else skip=true (or an error if --server named it explicitly).
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

// loadGenerateSource fetches and loads srv.Spec, returning
// errSpecRequiredForGenerate if unset or errSwagger2NotSupported for a
// Swagger 2.x document.
func loadGenerateSource(ctx context.Context, srv *config.Server) (*oastomcptool.SpecSource, error) {
	if srv.Spec == "" {
		return nil, errSpecRequiredForGenerate
	}
	source, err := oastomcptool.LoadSpecSource(ctx, srv.Spec)
	if err != nil {
		return nil, err
	}
	if source.Format == oastomcptool.SpecFormatSwagger2 {
		return nil, errSwagger2NotSupported
	}
	return source, nil
}

// buildGeneratedCatalog loads srv's live spec and builds the would-be
// generated catalog for it, shared by "generate" and "generate --check".
func buildGeneratedCatalog(
	ctx context.Context, srv *config.Server, generatedAt time.Time,
) (*oastomcptool.GeneratedCatalog, error) {
	source, err := loadGenerateSource(ctx, srv)
	if err != nil {
		return nil, err
	}

	registry, err := mcpsrv.BuildCatalog(ctx, &http.Client{}, source, srv.BaseURL, srv.ExtraHeaders)
	if err != nil {
		return nil, err
	}

	return oastomcptool.NewGeneratedCatalog(
		ctx, source, mcpsrv.GeneratedTools(registry.Definitions()), "manifold "+version.Version,
		generatedAt,
	)
}

// generateOne builds srv's catalog and writes it to outPath, returning the
// tool count.
func generateOne(ctx context.Context, srv *config.Server, outPath string) (int, error) {
	g, err := buildGeneratedCatalog(ctx, srv, time.Now())
	if err != nil {
		return 0, err
	}
	if err := writeGeneratedFileAtomic(outPath, g); err != nil {
		return 0, err
	}
	return len(g.Tools), nil
}

// writeGeneratedFileAtomic writes g to path via a temp file + rename, so a
// failure partway through never leaves a truncated file at path.
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
	// 0644: generated tools files are committed and meant to be world-readable.
	if err := os.Chmod(tmpPath, 0o644); err != nil { //nolint: gosec
		return fmt.Errorf("set permissions on temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file into place: %w", err)
	}
	return nil
}

// generatedCatalogDrift describes how the would-be generated catalog differs
// from what's on disk at a server's output path.
type generatedCatalogDrift struct {
	Missing              bool
	SpecChanged          bool
	OldSHA256, NewSHA256 string
	// EmbeddedSpecChanged catches a changed or hand-edited "spec" section
	// even when source.sha256 and the tools section are unchanged.
	EmbeddedSpecChanged bool
	Tools               mcpsrv.GeneratedToolsDiff
}

// empty reports whether d found no drift at all.
func (d generatedCatalogDrift) empty() bool {
	return !d.Missing && !d.SpecChanged && !d.EmbeddedSpecChanged && d.Tools.Empty()
}

// checkOne builds the would-be generated catalog for srv and compares it
// against the existing file at outPath.
func checkOne(
	ctx context.Context, srv *config.Server, outPath string,
) (generatedCatalogDrift, error) {
	next, err := buildGeneratedCatalog(ctx, srv, time.Now())
	if err != nil {
		return generatedCatalogDrift{}, err
	}

	f, err := os.Open(outPath) //nolint: gosec // outPath comes from config/flags, not a request
	if err != nil {
		if os.IsNotExist(err) {
			return generatedCatalogDrift{Missing: true}, nil
		}
		return generatedCatalogDrift{}, fmt.Errorf("read %s: %w", outPath, err)
	}
	defer f.Close()

	current, err := oastomcptool.ReadGeneratedCatalog(f)
	if err != nil {
		return generatedCatalogDrift{}, fmt.Errorf("read %s: %w", outPath, err)
	}

	drift := generatedCatalogDrift{Tools: mcpsrv.DiffGeneratedTools(current.Tools, next.Tools)}
	if current.Source.SHA256 != next.Source.SHA256 {
		drift.SpecChanged = true
		drift.OldSHA256 = current.Source.SHA256
		drift.NewSHA256 = next.Source.SHA256
	}

	// Compared as canonical JSON so YAML-decoded ints and JSON floats don't
	// register as a difference.
	sameSpec, err := mcpsrv.EqualAsJSON(current.Spec, next.Spec)
	if err != nil {
		return generatedCatalogDrift{}, fmt.Errorf("compare embedded spec: %w", err)
	}
	drift.EmbeddedSpecChanged = !sameSpec

	return drift, nil
}

// runOpenAPIGenerateCheck compares each selected server's would-be generated
// catalog against the file on disk without writing it, returning a non-nil
// error if any server had drift or failed to load.
func runOpenAPIGenerateCheck(cmd *cobra.Command, serverFilter, output string) error {
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
	driftCount := 0
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

		drift, err := checkOne(ctx, srv, outPath)
		switch {
		case errors.Is(err, errSwagger2NotSupported):
			fmt.Fprintf(stderr, "server %q: %v, skipping\n", name, err)
		case err != nil:
			fmt.Fprintf(stderr, "server %q: %v\n", name, err)
			errs = append(errs, fmt.Errorf("server %q: %w", name, err))
		case drift.Missing:
			driftCount++
			fmt.Fprintf(
				stdout,
				"server %q: %s is missing (run \"manifold openapi generate\")\n",
				name, outPath,
			)
		case drift.empty():
			fmt.Fprintf(stdout, "server %q: up to date (%s)\n", name, outPath)
		default:
			driftCount++
			writeDriftReport(stdout, name, outPath, drift)
		}
	}

	if driftCount > 0 {
		errs = append(errs, fmt.Errorf("drift detected in %d server(s)", driftCount))
	}
	return errors.Join(errs...)
}

// writeDriftReport prints the "drift detected" header and one line per
// difference.
func writeDriftReport(w io.Writer, name, outPath string, drift generatedCatalogDrift) {
	fmt.Fprintf(w, "server %q: drift detected (%s)\n", name, outPath)
	if drift.SpecChanged {
		fmt.Fprintf(
			w, "  spec changed (sha256 %s… → %s…)\n",
			shortSHA256(drift.OldSHA256), shortSHA256(drift.NewSHA256),
		)
	}
	if drift.EmbeddedSpecChanged {
		fmt.Fprintln(w, "  embedded spec differs from what the live spec produces")
	}

	added := slices.Clone(drift.Tools.Added)
	sort.Slice(added, func(i, j int) bool { return added[i].Name < added[j].Name })
	for _, t := range added {
		fmt.Fprintf(w, "  + added: %s (%s)\n", t.Name, t.Operation)
	}

	removed := slices.Clone(drift.Tools.Removed)
	sort.Slice(removed, func(i, j int) bool { return removed[i].Name < removed[j].Name })
	for _, t := range removed {
		fmt.Fprintf(w, "  - removed: %s (%s)\n", t.Name, t.Operation)
	}

	changed := slices.Clone(drift.Tools.Changed)
	sort.Slice(changed, func(i, j int) bool { return changed[i].Name < changed[j].Name })
	for _, c := range changed {
		fmt.Fprintf(w, "  ~ changed: %s (%s)\n", c.Name, strings.Join(c.Fields, ", "))
	}

	fmt.Fprintln(w, `  run "manifold openapi generate" to update`)
}

// shortSHA256 returns the first 8 hex characters of a sha256 hex digest.
func shortSHA256(sum string) string {
	if len(sum) > 8 {
		return sum[:8]
	}
	return sum
}
