package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

type balanceResp struct {
	Tenant   string `json:"tenant"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
	State    string `json:"state"`
	AsOf     string `json:"as_of"`
}

func balanceCmd() *cobra.Command {
	var tenant string

	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Show wallet balance for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)

			resp, err := http.Get(apiURL + "/balance?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var b balanceResp
			if err := json.Unmarshal(body, &b); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("Tenant:    %s\n", b.Tenant)
			fmt.Printf("Balance:   %s %s\n", b.Balance, b.Currency)
			fmt.Printf("State:     %s\n", b.State)
			fmt.Printf("As of:     %s\n", b.AsOf)
			return nil
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}
