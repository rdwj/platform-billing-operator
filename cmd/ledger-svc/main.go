package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/ledger/grpcapi"
	"github.com/rdwj/platform-billing-operator/internal/ledger/httpapi"
)

func main() {
	dsn := os.Getenv("LEDGER_DSN")
	if dsn == "" {
		log.Fatal("LEDGER_DSN is required")
	}
	listenGRPC := os.Getenv("LEDGER_LISTEN")
	if listenGRPC == "" {
		listenGRPC = ":50051"
	}
	listenHTTP := os.Getenv("LEDGER_HTTP_LISTEN")
	if listenHTTP == "" {
		listenHTTP = ":8080"
	}
	migrationsDir := os.Getenv("LEDGER_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "/migrations"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := ledger.NewStore(ctx, dsn)
	if err != nil {
		log.Fatalf("connect to postgres: %v", err)
	}
	defer store.Close()

	log.Printf("running migrations from %s", migrationsDir)
	if err := store.RunMigrations(migrationsDir); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	ingester, err := ledger.NewIngester(store)
	if err != nil {
		log.Fatalf("create ingester: %v", err)
	}
	querier := ledger.NewQuerier(store)

	// gRPC server
	grpcSrv := grpcapi.NewServer(ingester, querier)
	grpcLis, err := net.Listen("tcp", listenGRPC)
	if err != nil {
		log.Fatalf("listen gRPC on %s: %v", listenGRPC, err)
	}

	// K8s client for pricing endpoint (PriceBook lookup).
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(billingv1alpha1.AddToScheme(scheme))

	var k8sReader client.Reader
	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Printf("WARNING: no kubeconfig available, /v1/pricing will return 503: %v", err)
	} else {
		k8sClient, clientErr := client.New(cfg, client.Options{Scheme: scheme})
		if clientErr != nil {
			log.Printf("WARNING: failed to create K8s client, /v1/pricing will return 503: %v", clientErr)
		} else {
			k8sReader = k8sClient
		}
	}

	billingNS := os.Getenv("BILLING_NAMESPACE")
	if billingNS == "" {
		billingNS = "billing-system"
	}

	// HTTP server (convenience endpoint for WASM capture filter)
	httpHandler := httpapi.NewServer(ingester, store, k8sReader, billingNS)
	httpSrv := &http.Server{
		Addr:    listenHTTP,
		Handler: httpHandler,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "received %s, shutting down\n", sig)
		grpcSrv.GracefulStop()
		httpSrv.Shutdown(ctx) //nolint:errcheck
		cancel()
	}()

	go func() {
		log.Printf("ledger-svc HTTP listening on %s", listenHTTP)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http serve: %v", err)
		}
	}()

	log.Printf("ledger-svc gRPC listening on %s", listenGRPC)
	if err := grpcSrv.Serve(grpcLis); err != nil {
		log.Fatalf("grpc serve: %v", err)
	}
}
