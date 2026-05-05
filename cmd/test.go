package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bnaylor/vibecop/internal/evaluator"
	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a probe request to the configured endpoint",
	Long: `Test the LLM endpoint with a minimal probe request and report latency.
Respects the configured api_format and model settings.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := VibeCopConfig()

		if cfg.Model.Endpoint == "" {
			return fmt.Errorf("no endpoint configured — set [model].endpoint in config.toml")
		}

		fmt.Fprintf(os.Stderr, "endpoint: %s\n", cfg.Model.Endpoint)
		fmt.Fprintf(os.Stderr, "format:   %s\n", cfg.Model.APIFormat)
		fmt.Fprintf(os.Stderr, "model:    %s\n", cfg.Model.Model)
		fmt.Fprintf(os.Stderr, "timeout:  %d ms\n\n", cfg.Daemon.TimeoutMs)

		client := evaluator.New(
			cfg.Model.Endpoint,
			cfg.Model.APIKey,
			cfg.Model.APIFormat,
			cfg.Model.Model,
			time.Duration(cfg.Daemon.TimeoutMs)*time.Millisecond,
		)

		req := evaluator.ToolRequest{
			Tool:  "Read",
			Input: "vibecop connectivity probe",
		}

		fmt.Fprintf(os.Stderr, "sending probe request...\n")

		ctx, cancel := context.WithTimeout(context.Background(), client.Timeout())
		defer cancel()

		start := time.Now()
		v, err := client.Evaluate(ctx, req, evaluator.BaselinePrompt)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "\nFAILED after %v\n", elapsed.Round(time.Millisecond))
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "\nOK in %v\n", elapsed.Round(time.Millisecond))
		fmt.Fprintf(os.Stderr, "verdict: %s\n", v.Verdict)
		if v.Reason != "" {
			fmt.Fprintf(os.Stderr, "reason:  %s\n", v.Reason)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(testCmd)
}
