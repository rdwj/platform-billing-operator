package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/usagesink"
)

func sinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sink",
		Short: "Usage sink (OpenMeter) operations",
	}
	cmd.AddCommand(sinkStatusCmd())
	cmd.AddCommand(sinkTestIngestCmd())
	return cmd
}

func newSinkClient(kubeconfig string) (client.Client, error) {
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering billing scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering core scheme: %w", err)
	}

	k8s, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return k8s, nil
}

func sinkStatusCmd() *cobra.Command {
	var namespace string
	var kubeconfig string
	var allNamespaces bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show usage sink connector status with live health checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			k8s, err := newSinkClient(kubeconfig)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var connectors billingv1alpha1.BillingConnectorList
			opts := []client.ListOption{}
			if !allNamespaces && namespace != "" {
				opts = append(opts, client.InNamespace(namespace))
			}
			if err := k8s.List(ctx, &connectors, opts...); err != nil {
				return fmt.Errorf("listing BillingConnectors: %w", err)
			}

			// Filter to openmeter connectors only.
			var sinks []billingv1alpha1.BillingConnector
			for i := range connectors.Items {
				if connectors.Items[i].Spec.Type == billingv1alpha1.ConnectorTypeOpenMeter {
					sinks = append(sinks, connectors.Items[i])
				}
			}

			if len(sinks) == 0 {
				fmt.Println("No OpenMeter sink connectors found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if allNamespaces {
				_, _ = fmt.Fprintln(w, "NAMESPACE\tNAME\tENDPOINT\tCONNECTED\tHEALTH\tMETERS\tAGE")
			} else {
				_, _ = fmt.Fprintln(w, "NAME\tENDPOINT\tCONNECTED\tHEALTH\tMETERS\tAGE")
			}

			for i := range sinks {
				c := &sinks[i]
				connected := fmt.Sprintf("%v", c.Status.Connected)
				age := formatAge(c.CreationTimestamp)

				endpoint := "-"
				meters := "0"
				if c.Spec.UsageSink != nil {
					endpoint = c.Spec.UsageSink.Endpoint
					meters = strconv.Itoa(len(c.Spec.UsageSink.Meters))
				}

				health := "-"
				if c.Status.Connected && c.Spec.UsageSink != nil {
					apiKey, secretErr := readSinkAPIKey(ctx, k8s, c)
					if secretErr != nil {
						health = "Secret error: " + truncate(secretErr.Error(), 40)
					} else {
						backend := usagesink.NewOpenMeterBackend(c.Spec.UsageSink.Endpoint, apiKey)
						if err := backend.HealthCheck(ctx); err != nil {
							health = "Unhealthy: " + truncate(err.Error(), 40)
						} else {
							health = "Healthy"
						}
					}
				}

				if allNamespaces {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						c.Namespace, c.Name, endpoint, connected, health, meters, age)
				} else {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						c.Name, endpoint, connected, health, meters, age)
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

func sinkTestIngestCmd() *cobra.Command {
	var namespace string
	var kubeconfig string

	cmd := &cobra.Command{
		Use:   "test-ingest <connector-name>",
		Short: "Send a synthetic usage event to verify sink connectivity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			connectorName := args[0]

			k8s, err := newSinkClient(kubeconfig)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			var bc billingv1alpha1.BillingConnector
			if err := k8s.Get(ctx, types.NamespacedName{
				Name:      connectorName,
				Namespace: namespace,
			}, &bc); err != nil {
				return fmt.Errorf("fetching BillingConnector %q: %w", connectorName, err)
			}

			if bc.Spec.Type != billingv1alpha1.ConnectorTypeOpenMeter {
				return fmt.Errorf("connector %q is type %q, expected %q",
					connectorName, bc.Spec.Type, billingv1alpha1.ConnectorTypeOpenMeter)
			}

			if bc.Spec.UsageSink == nil {
				return fmt.Errorf("connector %q has no usageSink configuration", connectorName)
			}

			apiKey, err := readSinkAPIKey(ctx, k8s, &bc)
			if err != nil {
				return err
			}

			backend := usagesink.NewOpenMeterBackend(bc.Spec.UsageSink.Endpoint, apiKey)

			event := usagesink.UsageEvent{
				RecordID:         "test-" + strconv.FormatInt(time.Now().Unix(), 10),
				TenantID:         "test-tenant",
				Timestamp:        time.Now().UTC(),
				EventType:        "test",
				Model:            "test-model",
				PromptTokens:     decimal.NewFromInt(100),
				CompletionTokens: decimal.NewFromInt(50),
			}

			ingested, err := backend.IngestEvents(ctx, []usagesink.UsageEvent{event})
			if err != nil {
				return fmt.Errorf("ingestion failed: %w", err)
			}

			fmt.Printf("Successfully ingested %d test event to %s\n", ingested, connectorName)
			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace of the BillingConnector (required)")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")

	return cmd
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// readSinkAPIKey reads the "apiKey" field from the credentials Secret
// referenced by a BillingConnector. An empty key is not an error (self-hosted
// OpenMeter instances may not require authentication).
func readSinkAPIKey(ctx context.Context, k8s client.Client, bc *billingv1alpha1.BillingConnector) (string, error) {
	var secret corev1.Secret
	if err := k8s.Get(ctx, types.NamespacedName{
		Name:      bc.Spec.CredentialsSecretRef.Name,
		Namespace: bc.Namespace,
	}, &secret); err != nil {
		return "", fmt.Errorf("reading credentials secret %q: %w",
			bc.Spec.CredentialsSecretRef.Name, err)
	}

	return string(secret.Data["apiKey"]), nil
}
