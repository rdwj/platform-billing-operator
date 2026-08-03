package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func pspCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "psp",
		Short: "Payment service provider operations",
	}
	cmd.AddCommand(pspStatusCmd())
	return cmd
}

func pspStatusCmd() *cobra.Command {
	var namespace string
	var kubeconfig string
	var allNamespaces bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show BillingConnector status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if kubeconfig == "" {
				kubeconfig = clientcmd.RecommendedHomeFile
			}

			config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}

			scheme := runtime.NewScheme()
			if err := billingv1alpha1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("registering billing scheme: %w", err)
			}

			k8s, err := client.New(config, client.Options{Scheme: scheme})
			if err != nil {
				return fmt.Errorf("creating client: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var connectors billingv1alpha1.BillingConnectorList
			opts := []client.ListOption{}
			if !allNamespaces && namespace != "" {
				opts = append(opts, client.InNamespace(namespace))
			}
			if err := k8s.List(ctx, &connectors, opts...); err != nil {
				return fmt.Errorf("listing BillingConnectors: %w", err)
			}

			if len(connectors.Items) == 0 {
				fmt.Println("No BillingConnectors found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if allNamespaces {
				fmt.Fprintln(w, "NAMESPACE\tNAME\tTYPE\tCONNECTED\tCREDENTIALS\tWEBHOOKS 24H\tSIG FAILURES\tAGE")
			} else {
				fmt.Fprintln(w, "NAME\tTYPE\tCONNECTED\tCREDENTIALS\tWEBHOOKS 24H\tSIG FAILURES\tAGE")
			}

			for i := range connectors.Items {
				c := &connectors.Items[i]
				creds := credentialsStatus(c)
				webhooks := "-"
				sigFail := "-"
				if c.Status.WebhookStats != nil {
					webhooks = fmt.Sprintf("%d", c.Status.WebhookStats.Received24h)
					sigFail = fmt.Sprintf("%d", c.Status.WebhookStats.SignatureFailures)
				}
				age := formatAge(c.CreationTimestamp)
				connected := fmt.Sprintf("%v", c.Status.Connected)

				if allNamespaces {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						c.Namespace, c.Name, c.Spec.Type, connected, creds, webhooks, sigFail, age)
				} else {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						c.Name, c.Spec.Type, connected, creds, webhooks, sigFail, age)
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace to list connectors in")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "list connectors across all namespaces")

	return cmd
}

func credentialsStatus(c *billingv1alpha1.BillingConnector) string {
	for _, cond := range c.Status.Conditions {
		if cond.Type == billingv1alpha1.ConditionCredentialsValid {
			if cond.Status == metav1.ConditionTrue {
				return "Valid"
			}
			return cond.Reason
		}
	}
	return "Unknown"
}

func formatAge(ts metav1.Time) string {
	d := time.Since(ts.Time)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
