package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/spf13/cobra"
)

const openAPIDryRunLong = "Checks whether a single OpenAPI 3.x / Swagger 2.x file can be loaded, " +
	"without a config file and without starting the gateway. The format is taken from " +
	"the file name (<name>.openapi.yaml / <name>.swagger.yaml, .yml also accepted), or " +
	"else from the document's top-level \"openapi\" / \"swagger\" version field. The " +
	"file is then turned into MCP tools the same way the gateway does at startup, and " +
	"the result is printed. Exits non-zero if the file cannot be loaded or any tool " +
	"would be skipped by the gateway. No request is sent to the API."

func newOpenAPIDryRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dry-run <file>",
		Short: "Check whether an OpenAPI / Swagger file can be loaded and turned into MCP tools",
		Long:  openAPIDryRunLong,
		Args:  cobra.ExactArgs(1),
		// 単一の spec ファイルだけを対象にするため、root の設定ファイルの読み込みを行わない。
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenAPIDryRun(cmd, args[0])
		},
	}
}

// detectedSpecFormat is the format a dry-run file was loaded as, and how it
// was chosen.
type detectedSpecFormat struct {
	Format  oastomcptool.SpecFormat
	Version string
	// FromFileName is true when the file name chose the format.
	FromFileName bool
}

// String renders d as e.g. "OpenAPI 3.0.3 (from file name)".
func (d detectedSpecFormat) String() string {
	name := "OpenAPI"
	if d.Format == oastomcptool.SpecFormatSwagger2 {
		name = "Swagger"
	}
	source := "from version field"
	if d.FromFileName {
		source = "from file name"
	}
	return fmt.Sprintf("%s %s (%s)", name, d.Version, source)
}

// detectDryRunFormat chooses the format for path: the file name's
// .openapi / .swagger suffix if it has one, else the version field of raw.
// A file name that disagrees with the document's version field is an error,
// since the document cannot be loaded as the format its name declares.
func detectDryRunFormat(path string, raw []byte) (detectedSpecFormat, error) {
	byName, named := oastomcptool.SpecFormatFromFileName(path)
	byVersion, version, err := oastomcptool.DetectSpecFormat(raw)
	if !named {
		if err != nil {
			return detectedSpecFormat{}, fmt.Errorf("detect format: %w", err)
		}
		return detectedSpecFormat{Format: byVersion, Version: version}, nil
	}

	want := "OpenAPI 3.x"
	if byName == oastomcptool.SpecFormatSwagger2 {
		want = "Swagger 2.x"
	}
	if err != nil {
		return detectedSpecFormat{}, fmt.Errorf(
			"file name declares %s, but the document does not: %w", want, err,
		)
	}
	if byVersion != byName {
		field := "openapi"
		if byVersion == oastomcptool.SpecFormatSwagger2 {
			field = "swagger"
		}
		return detectedSpecFormat{}, fmt.Errorf(
			"file name declares %s, but the document declares %s: %q", want, field, version,
		)
	}
	return detectedSpecFormat{Format: byName, Version: version, FromFileName: true}, nil
}

// runOpenAPIDryRun loads path, builds its MCP tools the way the gateway
// does, and prints whether it loaded ("OK" / "NG") with the tools.
func runOpenAPIDryRun(cmd *cobra.Command, path string) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()

	fail := func(err error) error {
		fmt.Fprintf(stdout, "NG: %s\n", path)
		return fmt.Errorf("%s: %w", path, err)
	}

	raw, err := os.ReadFile(path) //nolint: gosec // path is the operator's own argument
	if err != nil {
		return fail(fmt.Errorf("read file: %w", err))
	}
	format, err := detectDryRunFormat(path, raw)
	if err != nil {
		return fail(err)
	}
	source, err := oastomcptool.ParseSpecSource(ctx, raw, path, format.Format)
	if err != nil {
		return fail(fmt.Errorf("load as %s: %w", format, err))
	}
	report, err := mcpsrv.DryRunCatalog(ctx, source)
	if err != nil {
		return fail(fmt.Errorf("build tools: %w", err))
	}

	status := "OK"
	if len(report.Invalid) > 0 {
		status = "NG"
	}
	fmt.Fprintf(stdout, "%s: %s\n", status, path)
	writeDryRunReport(stdout, format, report)

	if report.BaseURLErr != nil {
		fmt.Fprintf(stderr, "warning: %v\n", report.BaseURLErr)
	}
	if len(report.Tools) == 0 && len(report.Invalid) == 0 {
		fmt.Fprintln(stderr, "warning: the spec has no operations, so no tools would be served")
	}
	if len(report.Invalid) > 0 {
		return fmt.Errorf(
			"%s: %s would be skipped by the gateway",
			path, plural(len(report.Invalid), "tool"),
		)
	}
	return nil
}

// writeDryRunReport prints the format, base URL and tool count, the tools
// the gateway would skip, then one table line per tool.
func writeDryRunReport(w io.Writer, format detectedSpecFormat, report *mcpsrv.DryRunReport) {
	baseURL := report.BaseURL
	if report.BaseURLErr != nil {
		baseURL = "(not derivable from the spec; set mcpServers.<name>.baseURL)"
	}
	fmt.Fprintf(w, "  format:   %s\n", format)
	fmt.Fprintf(w, "  base URL: %s\n", baseURL)
	fmt.Fprintf(w, "  tools:    %d\n", len(report.Tools))
	if len(report.Invalid) > 0 {
		fmt.Fprintf(w, "  invalid:  %d\n", len(report.Invalid))
		for _, t := range report.Invalid {
			fmt.Fprintf(w, "    - %s: %v\n", t.Name, t.Err)
		}
	}
	if len(report.Tools) == 0 {
		return
	}

	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tOPERATION\tDESCRIPTION")
	for _, d := range report.Tools {
		desc := sanitizeDescription(d.Description)
		if d.BinaryResponse {
			desc += " [binary]"
		}
		fmt.Fprintf(tw, "%s\t%s %s\t%s\n", d.Name, d.Method, d.Path, desc)
	}
	_ = tw.Flush()
}
