package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/ledger/grpcapi"
)

func main() {
	dsn := os.Getenv("LEDGER_DSN")
	if dsn == "" {
		log.Fatal("LEDGER_DSN is required")
	}
	listen := os.Getenv("LEDGER_LISTEN")
	if listen == "" {
		listen = ":50051"
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

	srv := grpcapi.NewServer(ingester, querier)

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen on %s: %v", listen, err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "received %s, shutting down\n", sig)
		srv.GracefulStop()
		cancel()
	}()

	log.Printf("ledger-svc listening on %s", listen)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
