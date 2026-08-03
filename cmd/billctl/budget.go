package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

func budgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Budget policy operations",
	}
	cmd.AddCommand(budgetListCmd())
	cmd.AddCommand(budgetGetCmd())
	return cmd
}

func budgetListCmd() *cobra.Command {
	var namespace string
	var kubeconfig string
	var allNamespaces bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List BudgetPolicy resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			k8s, err := newBudgetClient(kubeconfig)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var policies billingv1alpha1.BudgetPolicyList
			opts := []client.ListOption{}
			if !allNamespaces && namespace != "" {
				opts = append(opts, client.InNamespace(namespace))
			}
			if err := k8s.List(ctx, &policies, opts...); err != nil {
				return fmt.Errorf("listing BudgetPolicies: %w", err)
			}

			if len(policies.Items) == 0 {
				fmt.Println("No BudgetPolicies found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if allNamespaces {
				_, _ = fmt.Fprintln(w, "NAMESPACE\tNAME\tTENANT\tSPEND\tCAPS\tFIRED\tSTATUS\tAGE")
			} else {
				_, _ = fmt.Fprintln(w, "NAME\tTENANT\tSPEND\tCAPS\tFIRED\tSTATUS\tAGE")
			}

			for i := range policies.Items {
				bp := &policies.Items[i]
				spend := bp.Status.CurrentSpend
				if spend == "" {
					spend = "-"
				}
				fired := fmt.Sprintf("%d", len(bp.Status.FiredThresholds))
				status := budgetStatus(bp)
				age := formatAge(bp.CreationTimestamp)
				caps := fmt.Sprintf("%d", len(bp.Spec.Caps))

				if allNamespaces {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						bp.Namespace, bp.Name, bp.Spec.TenantRef.Name,
						spend, caps, fired, status, age)
				} else {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						bp.Name, bp.Spec.TenantRef.Name,
						spend, caps, fired, status, age)
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "platform-billing", "namespace to list in")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "list across all namespaces")

	return cmd
}

func budgetGetCmd() *cobra.Command {
	var namespace string
	var kubeconfig string

	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Show BudgetPolicy details with spend vs. cap breakdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			k8s, err := newBudgetClient(kubeconfig)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var bp billingv1alpha1.BudgetPolicy
			if err := k8s.Get(ctx, types.NamespacedName{
				Name:      args[0],
				Namespace: namespace,
			}, &bp); err != nil {
				return fmt.Errorf("getting BudgetPolicy: %w", err)
			}

			fmt.Printf("Name:      %s\n", bp.Name)
			fmt.Printf("Namespace: %s\n", bp.Namespace)
			fmt.Printf("Tenant:    %s\n", bp.Spec.TenantRef.Name)
			fmt.Printf("Spend:     %s\n", orDash(bp.Status.CurrentSpend))
			fmt.Println()

			for i, cap := range bp.Spec.Caps {
				periodEnd := "-"
				for _, cpe := range bp.Status.CapPeriodEnds {
					if cpe.CapIndex == int32(i) {
						periodEnd = cpe.PeriodEnd.Format(time.RFC3339)
						break
					}
				}
				fmt.Printf("Cap %d: %s %s (period ends: %s)\n", i, cap.Amount, cap.Period, periodEnd)
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "  PCT\tACTION\tFIRED")
				for _, t := range cap.Thresholds {
					fired := "-"
					for _, ft := range bp.Status.FiredThresholds {
						if ft.CapIndex == int32(i) && ft.Pct == t.Pct {
							fired = ft.FiredAt.Format(time.RFC3339)
							break
						}
					}
					_, _ = fmt.Fprintf(w, "  %d%%\t%s\t%s\n", t.Pct, t.Action, fired)
				}
				_ = w.Flush()
				fmt.Println()
			}

			if len(bp.Status.Conditions) > 0 {
				fmt.Println("Conditions:")
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "  TYPE\tSTATUS\tREASON\tMESSAGE")
				for _, c := range bp.Status.Conditions {
					_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
						c.Type, c.Status, c.Reason, c.Message)
				}
				_ = w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "platform-billing", "namespace")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")

	return cmd
}

func newBudgetClient(kubeconfig string) (client.Client, error) {
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
	k8s, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return k8s, nil
}

func budgetStatus(bp *billingv1alpha1.BudgetPolicy) string {
	if cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionThresholdFired); cond != nil {
		if cond.Status == metav1.ConditionTrue {
			return "ThresholdFired"
		}
	}
	if cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionWithinBudget); cond != nil {
		if cond.Status == metav1.ConditionTrue {
			return "WithinBudget"
		}
	}
	if cond := apimeta.FindStatusCondition(bp.Status.Conditions, billingv1alpha1.ConditionEvaluating); cond != nil {
		if cond.Status == metav1.ConditionFalse {
			return cond.Reason
		}
	}
	return "Pending"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
