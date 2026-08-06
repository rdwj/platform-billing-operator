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

	"github.com/rdwj/platform-billing-operator/cmd/console/authn"
	"github.com/rdwj/platform-billing-operator/cmd/console/walkthroughs"
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
	providerProxy := &httputil.ReverseProxy{
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

	authMode := os.Getenv("AUTH_MODE")
	auth, err := authn.New(context.Background(), authn.Config{
		Mode:       authMode,
		RoleConfig: authn.ParseRoleConfig(),
	})
	if err != nil {
		log.Fatalf("auth init: %v", err)
	}

	mux := http.NewServeMux()

	// Provider routes: auth + role guard
	var providerHandler http.Handler = providerProxy
	if authMode != "" {
		providerHandler = authn.RequireProviderRole(providerProxy)
	}
	mux.Handle("/api/", auth.Middleware(providerHandler))

	// K8s client for tenant portal and walkthrough execution
	operatorNS := os.Getenv("OPERATOR_NAMESPACE")
	var k8sEnabled bool
	if operatorNS != "" {
		k8sClient, err := newK8sClient()
		if err != nil {
			log.Fatalf("k8s client init: %v", err)
		}
		k8sEnabled = true

		// Walkthrough routes (provider-only)
		defs, err := walkthroughs.Load()
		if err != nil {
			log.Fatalf("walkthrough loader: %v", err)
		}
		wtHandler := walkthroughs.NewHandler(defs, k8sClient, operatorNS)
		var wtRoutes http.Handler = wtHandler
		if authMode != "" {
			wtRoutes = authn.RequireProviderRole(wtHandler)
		}
		mux.Handle("/api/walkthroughs", auth.Middleware(wtRoutes))
		mux.Handle("/api/walkthroughs/", auth.Middleware(wtRoutes))
		log.Printf("walkthroughs loaded: %d definitions", len(defs))

		// Session endpoint: RBAC persona probing (no provider role guard)
		demoUser := os.Getenv("CONSOLE_DEMO_USER")
		lister := authn.NewK8sTenantLister(k8sClient, operatorNS)
		resolver := authn.NewTenantResolver(lister)
		sessionHandler := authn.NewSessionHandler(k8sClient, resolver, operatorNS, demoUser)
		mux.Handle("/api/session", auth.Middleware(sessionHandler))

		// Tenant portal routes
		tenantPortalEnabled := os.Getenv("TENANT_PORTAL_ENABLED")
		if tenantPortalEnabled != "false" && authMode != "" {
			tenantMW := authn.TenantMiddleware(resolver)

			portalProxy := &httputil.ReverseProxy{
				Rewrite: func(r *httputil.ProxyRequest) {
					r.SetURL(target)
					r.Out.URL.Path = "/v1/" + strings.TrimPrefix(r.In.URL.Path, "/portal/api/")

					tenant := authn.TenantFromContext(r.In.Context())
					q := r.Out.URL.Query()
					q.Set("tenant", tenant)
					r.Out.URL.RawQuery = q.Encode()
				},
			}

			mux.Handle("/portal/api/", auth.Middleware(tenantMW(portalProxy)))
			mux.Handle("/portal/", spaHandler)

			log.Printf("tenant portal enabled (namespace: %s)", operatorNS)
		}
	}
	_ = k8sEnabled

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
