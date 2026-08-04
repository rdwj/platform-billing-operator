package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rdwj/platform-billing-operator/console"
)

func main() {
	ledgerURL := os.Getenv("LEDGER_URL")
	if ledgerURL == "" {
		log.Fatal("LEDGER_URL is required (e.g. http://ledger-svc:8080)")
	}
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":9090"
	}

	target, err := url.Parse(ledgerURL)
	if err != nil {
		log.Fatalf("invalid LEDGER_URL: %v", err)
	}

	// Reverse proxy: /api/* -> ledger-svc /v1/*
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = "/v1/" + strings.TrimPrefix(r.In.URL.Path, "/api/")
		},
	}

	// SPA file server with fallback to index.html for client-side routing
	distFS, err := fs.Sub(console.DistFS, "dist")
	if err != nil {
		log.Fatalf("embed.FS sub: %v", err)
	}
	spaHandler := spaFileServer(http.FS(distFS))

	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n")) //nolint:errcheck
	})
	mux.Handle("/", spaHandler)

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "received %s, shutting down\n", sig)
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()

	log.Printf("console BFF listening on %s (proxying to %s)", listenAddr, ledgerURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http serve: %v", err)
	}
}

// spaFileServer serves static files from the embedded FS, falling back to
// index.html for paths that don't match a file (client-side routing).
func spaFileServer(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := fsys.Open(r.URL.Path)
		if err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		stat, err := f.Stat()
		_ = f.Close()
		if err != nil || stat.IsDir() {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
