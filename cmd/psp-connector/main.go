/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/psp"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	ledgerURL := flag.String("ledger-url", "", "base URL of ledger-svc (required)")
	flag.Parse()

	if *ledgerURL == "" {
		fmt.Fprintln(os.Stderr, "error: --ledger-url is required")
		os.Exit(1)
	}

	connectorName := os.Getenv("CONNECTOR_NAME")
	if connectorName == "" {
		fmt.Fprintln(os.Stderr, "error: CONNECTOR_NAME env var is required")
		os.Exit(1)
	}

	namespace := os.Getenv("PSP_NAMESPACE")
	if namespace == "" {
		fmt.Fprintln(os.Stderr, "error: PSP_NAMESPACE env var is required")
		os.Exit(1)
	}

	// Build K8s client.
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(billingv1alpha1.AddToScheme(scheme))

	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := envOr("KUBECONFIG", os.ExpandEnv("$HOME/.kube/config"))
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			slog.Error("failed to build K8s config", "error", err)
			os.Exit(1)
		}
	}

	k8sClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		slog.Error("failed to create K8s client", "error", err)
		os.Exit(1)
	}

	// Read BillingConnector CR and its credentials Secret.
	ctx := context.Background()

	var bc billingv1alpha1.BillingConnector
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: connectorName, Namespace: namespace}, &bc); err != nil {
		slog.Error("failed to read BillingConnector", "name", connectorName, "namespace", namespace, "error", err)
		os.Exit(1)
	}

	secretName := bc.Spec.CredentialsSecretRef.Name
	var secret corev1.Secret
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &secret); err != nil {
		slog.Error("failed to read credentials Secret", "name", secretName, "error", err)
		os.Exit(1)
	}

	// Construct PSP backend based on connector type.
	var backend psp.PSPBackend
	switch bc.Spec.Type {
	case "stripe":
		backend = psp.NewStripeBackend(string(secret.Data["secretKey"]), string(secret.Data["webhookSecret"]))
		slog.Info("using Stripe backend")
	case "webhook":
		backend = psp.NewWebhookBackend(string(secret.Data["hmacSecret"]))
		slog.Info("using webhook backend")
	default:
		slog.Error("unsupported connector type", "type", bc.Spec.Type)
		os.Exit(1)
	}

	ledger := &psp.HTTPLedgerClient{
		BaseURL: *ledgerURL,
	}

	k8sUpdater := &psp.RealK8sStatusUpdater{
		Client:    k8sClient,
		Namespace: namespace,
	}

	handler := &psp.WebhookHandler{
		Backend: backend,
		Ledger:  ledger,
		K8s:     k8sUpdater,
	}

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	server := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("psp-connector starting",
		"addr", *addr,
		"ledger_url", *ledgerURL,
		"connector", connectorName,
		"namespace", namespace,
		"type", bc.Spec.Type,
	)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCtx.Done()
	slog.Info("psp-connector shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
