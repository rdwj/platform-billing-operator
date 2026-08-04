package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var apiURL string

func main() {
	rootCmd := &cobra.Command{
		Use:   "billctl",
		Short: "CLI for the platform-billing-operator billing API",
	}

	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "http://localhost:8080/v1", "billing-api base URL")

	rootCmd.AddCommand(usageCmd())
	rootCmd.AddCommand(balanceCmd())
	rootCmd.AddCommand(pricingCmd())
	rootCmd.AddCommand(adjustCmd())
	rootCmd.AddCommand(keysCmd())
	rootCmd.AddCommand(pspCmd())
	rootCmd.AddCommand(budgetCmd())
	rootCmd.AddCommand(driftCmd())
	rootCmd.AddCommand(exportCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
