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

type focusExportResponse struct {
	Rows []focusRow `json:"rows"`
}

type focusRow struct {
	BilledCost        string `json:"BilledCost"`
	BillingCurrency   string `json:"BillingCurrency"`
	ChargePeriodStart string `json:"ChargePeriodStart"`
	ConsumedQuantity  string `json:"ConsumedQuantity"`
	SkuId             string `json:"SkuId"`
	SkuMeter          string `json:"SkuMeter"`
}

func exportCmd() *cobra.Command {
	var tenant, from, to, format string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export rated usage in FOCUS 1.2 format",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)
			params.Set("from", from)
			params.Set("to", to)

			if format == "json" || format == "csv" {
				params.Set("format", format)
			}

			resp, err := http.Get(apiURL + "/export?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			// JSON and CSV: print raw response body
			if format == "json" || format == "csv" {
				fmt.Print(string(body))
				return nil
			}

			// Table (default): unmarshal and tabulate
			var export focusExportResponse
			if err := json.Unmarshal(body, &export); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "PERIOD_START\tSKU\tMETER\tQUANTITY\tCOST\tCURRENCY\n")
			for _, r := range export.Rows {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.ChargePeriodStart, r.SkuId, r.SkuMeter,
					r.ConsumedQuantity, r.BilledCost, r.BillingCurrency)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&from, "from", "", "start time in RFC3339 format (required)")
	cmd.Flags().StringVar(&to, "to", "", "end time in RFC3339 format (required)")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table, json, csv")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
