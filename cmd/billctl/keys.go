package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func keysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys for a tenant",
	}

	cmd.AddCommand(keysListCmd())
	cmd.AddCommand(keysDisableCmd())
	cmd.AddCommand(keysEnableCmd())
	return cmd
}

func keysListCmd() *cobra.Command {
	var tenant string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API keys for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)

			resp, err := http.Get(apiURL + "/keys?" + params.Encode())
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var result struct {
				Tenant string `json:"tenant"`
				Keys   []struct {
					KeyName    string  `json:"key_name"`
					Enabled    bool    `json:"enabled"`
					Source     string  `json:"source"`
					CreatedAt  string  `json:"created_at"`
					DisabledAt *string `json:"disabled_at"`
				} `json:"keys"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "KEY\tENABLED\tSOURCE\tCREATED\tDISABLED\n")
			for _, k := range result.Keys {
				disabled := ""
				if k.DisabledAt != nil {
					disabled = *k.DisabledAt
				}
				fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\n",
					k.KeyName, k.Enabled, k.Source, k.CreatedAt, disabled)
			}
			w.Flush() //nolint:errcheck
			return nil
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}

func keysDisableCmd() *cobra.Command {
	var tenant, key string

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable an API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			return keysAction(tenant, key, "disable")
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&key, "key", "", "key name (required)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func keysEnableCmd() *cobra.Command {
	var tenant, key string

	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable an API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			return keysAction(tenant, key, "enable")
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&key, "key", "", "key name (required)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func keysAction(tenant, key, action string) error {
	reqBody, _ := json.Marshal(map[string]string{
		"tenant":   tenant,
		"key_name": key,
	})

	resp, err := http.Post(apiURL+"/keys/"+action, "application/json", bytes.NewReader(reqBody))
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
}
