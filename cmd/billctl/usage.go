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

type usageSeries struct {
	Tenant string       `json:"tenant"`
	Bucket string       `json:"bucket"`
	Points []usagePoint `json:"points"`
}

type usagePoint struct {
	Start      string             `json:"start"`
	SKU        string             `json:"sku"`
	Quantities map[string]float64 `json:"quantities"`
	Charged    string             `json:"charged"`
	Events     int                `json:"events"`
}

func usageCmd() *cobra.Command {
	var tenant, from, to, bucket, sku string

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Query bucketed usage/charge totals",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)
			params.Set("from", from)
			params.Set("to", to)
			params.Set("bucket", bucket)
			if sku != "" {
				params.Set("sku", sku)
			}

			resp, err := http.Get(apiURL + "/usage?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var series usageSeries
			if err := json.Unmarshal(body, &series); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "START\tSKU\tCHARGED\tEVENTS\n")
			for _, p := range series.Points {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", p.Start, p.SKU, p.Charged, p.Events)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&from, "from", "", "start time RFC3339 (required)")
	cmd.Flags().StringVar(&to, "to", "", "end time RFC3339 (required)")
	cmd.Flags().StringVar(&bucket, "bucket", "day", "aggregation bucket: hour, day, month")
	cmd.Flags().StringVar(&sku, "sku", "", "filter by model/SKU")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
