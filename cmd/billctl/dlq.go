package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type dlqEntry struct {
	ID         string          `json:"id"`
	Raw        json.RawMessage `json:"raw"`
	Reason     string          `json:"reason"`
	Source     string          `json:"source"`
	ReceivedAt string          `json:"receivedAt"`
	Attempts   int             `json:"attempts"`
	LastError  string          `json:"lastError,omitempty"`
}

type dlqReplayResp struct {
	Replayed int `json:"replayed"`
	Failed   int `json:"failed"`
	Errors   []struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	} `json:"errors"`
}

func dlqCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dlq",
		Short: "Manage dead-letter queue entries",
	}
	cmd.AddCommand(dlqListCmd())
	cmd.AddCommand(dlqReplayCmd())
	return cmd
}

func dlqListCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List unreplayed DLQ entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := fmt.Sprintf("%s/dlq?limit=%d", apiURL, limit)
			resp, err := http.Get(url)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var entries []dlqEntry
			if err := json.Unmarshal(body, &entries); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("No unreplayed DLQ entries.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "ID\tREASON\tSOURCE\tRECEIVED\tATTEMPTS\n")
			for _, e := range entries {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
					e.ID, e.Reason, e.Source, e.ReceivedAt, e.Attempts)
			}
			return w.Flush()
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 100, "max entries to return")
	return cmd
}

func dlqReplayCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "replay [--all | id1 id2 ...]",
		Short: "Replay DLQ entries through the ingest pipeline",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return fmt.Errorf("provide entry IDs or use --all")
			}

			reqBody := map[string]any{}
			if all {
				reqBody["all"] = true
			} else {
				reqBody["ids"] = args
			}

			payload, err := json.Marshal(reqBody)
			if err != nil {
				return fmt.Errorf("marshal request: %w", err)
			}

			resp, err := http.Post(apiURL+"/dlq/replay", "application/json", bytes.NewReader(payload))
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var result dlqReplayResp
			if err := json.Unmarshal(body, &result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("Replayed: %d  Failed: %d\n", result.Replayed, result.Failed)
			for _, e := range result.Errors {
				fmt.Printf("  %s: %s\n", e.ID, e.Error)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "replay all unreplayed entries (up to 1000)")
	return cmd
}
