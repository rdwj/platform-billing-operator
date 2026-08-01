package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

func pricingCmd() *cobra.Command {
	var tenant string

	cmd := &cobra.Command{
		Use:   "pricing",
		Short: "Show resolved PriceBook for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)

			resp, err := http.Get(apiURL + "/pricing?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			fmt.Println(string(body))
			return nil
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}
