// Command manifold-guardrail は、Manifold のガードレール（guardrail.url）の先に置く
// アダプタ。Manifold 本体とは別の Go モジュールにして、LLM や OCR の SDK の依存を
// 本体に持ち込まない。
//
// サブコマンド:
//
//	manifold-guardrail file   ファイル用ガードレール（ファイルをテキストに起こして、
//	                          下流のテキスト用ガードレールで判定する）
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nonchan7720/manifold/guardrails/internal/version"
)

const usage = `Usage: manifold-guardrail <command> [flags]

Commands:
  file    Serve the Generic Guardrail API for files: transcribe each file
          (LLM or an OCR service) and check the text with a downstream
          text guardrail.

  version Print the version.

Run "manifold-guardrail <command> -h" for the flags of a command.
`

func main() {
	os.Exit(mainExitCode())
}

func mainExitCode() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "manifold-guardrail:", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "file":
		return runFile(ctx, args[1:])
	case "version", "-v", "--version":
		fmt.Fprintln(os.Stdout, "manifold-guardrail", version.Version)
		return nil
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
