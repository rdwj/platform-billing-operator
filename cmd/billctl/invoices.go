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

type invoiceRow struct {
	InvoiceID   string `json:"invoice_id"`
	TenantID    string `json:"tenant_id"`
	Period      string `json:"period"`
	Status      string `json:"status"`
	Total       string `json:"total"`
	PSPRef      string `json:"psp_ref,omitempty"`
	FinalizedAt string `json:"finalized_at,omitempty"`
}

type invoiceLine struct {
	LineNo       int    `json:"line_no"`
	Description  string `json:"description"`
	SKU          string `json:"sku,omitempty"`
	Geo          string `json:"geo,omitempty"`
	QuantityType string `json:"quantity_type,omitempty"`
	Quantity     int64  `json:"quantity"`
	Rate         string `json:"rate,omitempty"`
	Amount       string `json:"amount"`
}

type invoiceDetail struct {
	invoiceRow
	Lines []invoiceLine `json:"lines"`
}

func invoicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invoices",
		Short: "Invoice management",
	}
	cmd.AddCommand(invoicesListCmd())
	cmd.AddCommand(invoicesGetCmd())
	return cmd
}

func invoicesListCmd() *cobra.Command {
	var tenant string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List invoices for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			params := url.Values{}
			params.Set("tenant", tenant)
			params.Set("limit", fmt.Sprintf("%d", limit))

			resp, err := http.Get(apiURL + "/invoices?" + params.Encode())
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
				Invoices []invoiceRow `json:"invoices"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "INVOICE_ID\tTENANT\tPERIOD\tSTATUS\tTOTAL\tFINALIZED_AT\n")
			for _, inv := range envelope.Invoices {
				finalizedAt := "-"
				if inv.FinalizedAt != "" {
					finalizedAt = inv.FinalizedAt
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					inv.InvoiceID, inv.TenantID, inv.Period, inv.Status, inv.Total, finalizedAt)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().IntVar(&limit, "limit", 20, "max number of invoices to return")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}

func invoicesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <invoice-id>",
		Short: "Show invoice details with line items",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := http.Get(apiURL + "/invoices/" + args[0])
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var inv invoiceDetail
			if err := json.Unmarshal(body, &inv); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("Invoice:     %s\n", inv.InvoiceID)
			fmt.Printf("Tenant:      %s\n", inv.TenantID)
			fmt.Printf("Period:      %s\n", inv.Period)
			fmt.Printf("Status:      %s\n", inv.Status)
			fmt.Printf("Total:       %s\n", inv.Total)
			if inv.PSPRef != "" {
				fmt.Printf("PSP Ref:     %s\n", inv.PSPRef)
			}
			if inv.FinalizedAt != "" {
				fmt.Printf("Finalized:   %s\n", inv.FinalizedAt)
			}

			if len(inv.Lines) > 0 {
				fmt.Println()
				w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
				_, _ = fmt.Fprintf(w, "LINE\tDESCRIPTION\tSKU\tQUANTITY\tRATE\tAMOUNT\n")
				for _, line := range inv.Lines {
					_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\n",
						line.LineNo, line.Description, line.SKU, line.Quantity, line.Rate, line.Amount)
				}
				_ = w.Flush()
			}
			return nil
		},
	}
	return cmd
}
