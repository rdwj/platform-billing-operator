package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

type adjustRequest struct {
	Tenant         string `json:"tenant"`
	Amount         string `json:"amount"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type adjustResponse struct {
	ID             string `json:"id"`
	Tenant         string `json:"tenant"`
	Amount         string `json:"amount"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
	CreatedAt      string `json:"created_at"`
}

func adjustCmd() *cobra.Command {
	var tenant, amount, kind, reason, idempotencyKey string

	cmd := &cobra.Command{
		Use:   "adjust",
		Short: "Create a manual credit/debit adjustment",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqBody, err := json.Marshal(adjustRequest{
				Tenant:         tenant,
				Amount:         amount,
				Kind:           kind,
				Reason:         reason,
				IdempotencyKey: idempotencyKey,
			})
			if err != nil {
				return fmt.Errorf("encode request: %w", err)
			}

			resp, err := http.Post(apiURL+"/adjustments", "application/json", bytes.NewReader(reqBody))
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode == http.StatusConflict {
				fmt.Fprintf(os.Stderr, "Conflict: idempotency key already used with different content\n")
				os.Exit(1)
			}
			if resp.StatusCode != http.StatusCreated {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var adj adjustResponse
			if err := json.Unmarshal(body, &adj); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Println("Adjustment created:")
			fmt.Printf("ID:        %s\n", adj.ID)
			fmt.Printf("Tenant:    %s\n", adj.Tenant)
			fmt.Printf("Amount:    %s\n", adj.Amount)
			fmt.Printf("Kind:      %s\n", adj.Kind)
			fmt.Printf("Created:   %s\n", adj.CreatedAt)
			return nil
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&amount, "amount", "", "decimal amount, e.g. 25.000000 (required)")
	cmd.Flags().StringVar(&kind, "kind", "",
		"adjustment kind (required): manual_credit, manual_debit, balance_carry_in, "+
			"writeoff, unmetered_gap, payment_credit, eviction_credit, sla_credit, refund")
	cmd.Flags().StringVar(&reason, "reason", "", "audit-log reason (required)")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "unique key, min 8 chars (required)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("amount")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("reason")
	_ = cmd.MarkFlagRequired("idempotency-key")
	return cmd
}
