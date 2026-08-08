package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type reservationsListResponse struct {
	Reservations []reservationResponse `json:"reservations"`
}

type reservationResponse struct {
	Name                       string                `json:"name"`
	Tenant                     string                `json:"tenant"`
	SKU                        string                `json:"sku"`
	Count                      int32                 `json:"count"`
	Geo                        string                `json:"geo"`
	Class                      string                `json:"class"`
	MinAllocationSeconds       *int32                `json:"minAllocationSeconds,omitempty"`
	EvictionCreditPerGpuSecond *string               `json:"evictionCreditPerGpuSecond,omitempty"`
	SloAdmissionSeconds        *int32                `json:"sloAdmissionSeconds,omitempty"`
	Window                     reservationWindowResp `json:"window"`
	MonthlyPrice               string                `json:"monthlyPrice"`
	State                      string                `json:"state"`
	CancelledAt                string                `json:"cancelledAt,omitempty"`
	CapacityCheck              *capacityCheckResp    `json:"capacityCheck,omitempty"`
}

type reservationWindowResp struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type capacityCheckResp struct {
	Available bool   `json:"available"`
	Pool      string `json:"pool,omitempty"`
}

type createReservationRequest struct {
	Tenant                     string  `json:"tenant"`
	SKU                        string  `json:"sku"`
	Count                      int32   `json:"count"`
	Geo                        string  `json:"geo"`
	Class                      string  `json:"class"`
	MinAllocationSeconds       *int32  `json:"minAllocationSeconds,omitempty"`
	EvictionCreditPerGpuSecond *string `json:"evictionCreditPerGpuSecond,omitempty"`
	SloAdmissionSeconds        *int32  `json:"sloAdmissionSeconds,omitempty"`
	Start                      string  `json:"start"`
	End                        string  `json:"end"`
	MonthlyPrice               string  `json:"monthlyPrice"`
}

// dateOnly parses an RFC3339 timestamp and returns YYYY-MM-DD.
// If parsing fails, it returns the original string unchanged.
func dateOnly(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Format("2006-01-02")
}

func reservationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reservations",
		Short: "Manage GPU reservations",
	}

	cmd.AddCommand(reservationsListCmd())
	cmd.AddCommand(reservationsGetCmd())
	cmd.AddCommand(reservationsCreateCmd())
	cmd.AddCommand(reservationsCancelCmd())
	return cmd
}

func reservationsListCmd() *cobra.Command {
	var tenant string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reservations",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := apiURL + "/reservations"
			if tenant != "" {
				endpoint += "?tenant=" + tenant
			}

			resp, err := http.Get(endpoint)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var listResp reservationsListResponse
			if err := json.Unmarshal(body, &listResp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "NAME\tTENANT\tSKU\tCOUNT\tCLASS\tSTATE\tSTART\tEND\n")
			for _, r := range listResp.Reservations {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
					r.Name, r.Tenant, r.SKU, r.Count, r.Class, r.State,
					dateOnly(r.Window.Start), dateOnly(r.Window.End))
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "filter by tenant ID")
	return cmd
}

func reservationsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get NAME",
		Short: "Get reservation details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := http.Get(apiURL + "/reservations/" + args[0])
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var r reservationResponse
			if err := json.Unmarshal(body, &r); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("Name:          %s\n", r.Name)
			fmt.Printf("Tenant:        %s\n", r.Tenant)
			fmt.Printf("SKU:           %s\n", r.SKU)
			fmt.Printf("Count:         %d\n", r.Count)
			fmt.Printf("Geo:           %s\n", r.Geo)
			fmt.Printf("Class:         %s\n", r.Class)
			if r.MinAllocationSeconds != nil {
				fmt.Printf("MinAlloc:      %ds\n", *r.MinAllocationSeconds)
			}
			if r.EvictionCreditPerGpuSecond != nil {
				fmt.Printf("EvictCredit:   %s/gpu-sec\n", *r.EvictionCreditPerGpuSecond)
			}
			if r.SloAdmissionSeconds != nil {
				fmt.Printf("SLO:           %ds\n", *r.SloAdmissionSeconds)
			}
			fmt.Printf("MonthlyPrice:  %s\n", r.MonthlyPrice)
			fmt.Printf("State:         %s\n", r.State)
			fmt.Printf("Window:        %s - %s\n", r.Window.Start, r.Window.End)
			if r.CancelledAt != "" {
				fmt.Printf("CancelledAt:   %s\n", r.CancelledAt)
			}
			if r.CapacityCheck != nil {
				fmt.Printf("CapacityCheck: available=%v", r.CapacityCheck.Available)
				if r.CapacityCheck.Pool != "" {
					fmt.Printf(" pool=%s", r.CapacityCheck.Pool)
				}
				fmt.Println()
			}
			return nil
		},
	}

	return cmd
}

func reservationsCreateCmd() *cobra.Command {
	var tenant, sku, geo, class, start, end, monthlyPrice, evictionCredit string
	var count, minAllocSeconds, sloAdmissionSeconds int32

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new reservation",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := createReservationRequest{
				Tenant:       tenant,
				SKU:          sku,
				Count:        count,
				Geo:          geo,
				Class:        class,
				Start:        start,
				End:          end,
				MonthlyPrice: monthlyPrice,
			}
			if cmd.Flags().Changed("min-allocation-seconds") {
				req.MinAllocationSeconds = &minAllocSeconds
			}
			if cmd.Flags().Changed("eviction-credit-per-gpu-second") {
				req.EvictionCreditPerGpuSecond = &evictionCredit
			}
			if cmd.Flags().Changed("slo-admission-seconds") {
				req.SloAdmissionSeconds = &sloAdmissionSeconds
			}
			reqBody, err := json.Marshal(req)
			if err != nil {
				return fmt.Errorf("encode request: %w", err)
			}

			resp, err := http.Post(apiURL+"/reservations", "application/json", bytes.NewReader(reqBody))
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusCreated {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			var r reservationResponse
			if err := json.Unmarshal(body, &r); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("Reservation %s created (state: %s)\n", r.Name, r.State)
			return nil
		},
	}

	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant ID (required)")
	cmd.Flags().StringVar(&sku, "sku", "", "GPU SKU (required)")
	cmd.Flags().Int32Var(&count, "count", 0, "number of GPUs (required)")
	cmd.Flags().StringVar(&geo, "geo", "", "geographic region (required)")
	cmd.Flags().StringVar(&class, "class", "", "Guaranteed or Preemptible (required)")
	cmd.Flags().Int32Var(&minAllocSeconds, "min-allocation-seconds", 0,
		"minimum allocation time in seconds (Preemptible only)")
	cmd.Flags().StringVar(&evictionCredit, "eviction-credit-per-gpu-second", "",
		"credit rate per GPU-second on eviction (Preemptible only, decimal)")
	cmd.Flags().Int32Var(&sloAdmissionSeconds, "slo-admission-seconds", 0,
		"max admission queue time in seconds (Guaranteed only, default 300)")
	cmd.Flags().StringVar(&start, "start", "", "start time RFC3339 (required)")
	cmd.Flags().StringVar(&end, "end", "", "end time RFC3339 (required)")
	cmd.Flags().StringVar(&monthlyPrice, "monthly-price", "", "decimal price per month (required)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("sku")
	_ = cmd.MarkFlagRequired("count")
	_ = cmd.MarkFlagRequired("geo")
	_ = cmd.MarkFlagRequired("class")
	_ = cmd.MarkFlagRequired("start")
	_ = cmd.MarkFlagRequired("end")
	_ = cmd.MarkFlagRequired("monthly-price")
	return cmd
}

func reservationsCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel NAME",
		Short: "Cancel a reservation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := http.Post(apiURL+"/reservations/"+args[0]+"/cancel", "application/json", nil)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close() //nolint:errcheck

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, body)
				os.Exit(1)
			}

			fmt.Printf("Reservation %s cancelled\n", args[0])
			return nil
		},
	}

	return cmd
}
