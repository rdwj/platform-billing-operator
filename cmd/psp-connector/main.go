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

	"github.com/rdwj/platform-billing-operator/internal/psp"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	ledgerURL := flag.String("ledger-url", "", "base URL of ledger-svc (required)")
	flag.Parse()

	if *ledgerURL == "" {
		fmt.Fprintln(os.Stderr, "error: --ledger-url is required")
		os.Exit(1)
	}

	// Phase 3: use recording/stub backends. Phase 4 will inject real
	// Stripe + HTTP LedgerClient + K8s client based on BillingConnector CR.
	backend := &psp.RecordingBackend{}
	ledger := &psp.StubLedgerClient{}
	k8s := &psp.RecordingK8sUpdater{}

	handler := &psp.WebhookHandler{
		Backend: backend,
		Ledger:  ledger,
		K8s:     k8s,
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("psp-connector starting", "addr", *addr, "ledger_url", *ledgerURL)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("psp-connector shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
