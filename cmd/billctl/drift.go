package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type driftReport struct {
	ID           int64  `json:"id"`
	TenantID     string `json:"tenant_id"`
	WindowStart  string `json:"window_start"`
	WindowEnd    string `json:"window_end"`
	LedgerTokens int64  `json:"ledger_tokens"`
	PromTokens   int64  `json:"prom_tokens"`
	DriftPct     string `json:"drift_pct"`
	TolerancePct string `json:"tolerance_pct"`
	Exceeded     bool   `json:"exceeded"`
	Signature    string `json:"signature"`
	CreatedAt    string `json:"created_at"`
}

func driftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Drift reconciliation reports",
	}
	cmd.AddCommand(driftListCmd())
	cmd.AddCommand(driftGetCmd())
	return cmd
}

func driftListCmd() *cobra.Command {
	var tenant string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List drift reports for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)
			params.Set("limit", fmt.Sprintf("%d", limit))

			resp, err := http.Get(apiURL + "/drift?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var envelope struct {
				Reports []driftReport `json:"reports"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "TENANT\tWINDOW_START\tWINDOW_END\tLEDGER\tPROM\tDRIFT%%\tEXCEEDED\n")
			for _, r := range envelope.Reports {
				exceeded := "-"
				if r.Exceeded {
					exceeded = "YES"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s%%\t%s\n",
					r.TenantID, r.WindowStart, r.WindowEnd,
					r.LedgerTokens, r.PromTokens, r.DriftPct, exceeded)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().IntVar(&limit, "limit", 20, "max number of reports to return")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}

func driftGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <tenant>",
		Short: "Show latest drift report for a tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", args[0])

			resp, err := http.Get(apiURL + "/drift/latest?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var r driftReport
			if err := json.Unmarshal(body, &r); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			exceeded := "NO"
			if r.Exceeded {
				exceeded = "YES"
			}

			fmt.Printf("Tenant:        %s\n", r.TenantID)
			fmt.Printf("Window:        %s → %s\n", r.WindowStart, r.WindowEnd)
			fmt.Printf("Ledger tokens: %d\n", r.LedgerTokens)
			fmt.Printf("Prom tokens:   %d\n", r.PromTokens)
			fmt.Printf("Drift:         %s%%\n", r.DriftPct)
			fmt.Printf("Tolerance:     %s%%\n", r.TolerancePct)
			fmt.Printf("Exceeded:      %s\n", exceeded)
			fmt.Printf("Signature:     %s\n", r.Signature)
			fmt.Printf("Created:       %s\n", r.CreatedAt)
			return nil
		},
	}

	return cmd
}
