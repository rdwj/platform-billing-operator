package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type tenantToken struct {
	tenantID string
	token    string
}

type config struct {
	mode          string
	url           string
	token         string
	concurrency   int
	duration      time.Duration
	model         string
	streamRatio   float64
	rps           int
	prompt        string
	maxTokens     int
	tlsSkipVerify bool
}

type result struct {
	latency    time.Duration
	err        error
	statusCode int
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "gateway", "gateway or ingest")
	flag.StringVar(&cfg.url, "url", "", "target URL (required)")
	flag.StringVar(&cfg.token, "token", "", "bearer token (gateway mode, single tenant)")
	var tokensFlag string
	flag.StringVar(&tokensFlag, "tokens", "", "comma-separated tenant_id:token pairs for multi-tenant round-robin")
	flag.IntVar(&cfg.concurrency, "concurrency", 10, "concurrent workers")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "test duration")
	flag.StringVar(&cfg.model, "model", "/mnt/models", "model name")
	flag.Float64Var(&cfg.streamRatio, "stream-ratio", 0.5, "fraction streaming 0.0-1.0")
	flag.IntVar(&cfg.rps, "rps", 0, "target RPS, 0=unlimited")
	flag.StringVar(&cfg.prompt, "prompt", "Say hello in five words", "prompt text")
	flag.IntVar(&cfg.maxTokens, "max-tokens", 20, "max completion tokens")
	flag.BoolVar(&cfg.tlsSkipVerify, "tls-skip-verify", true, "skip TLS verification")
	flag.Parse()

	if cfg.url == "" {
		fmt.Fprintln(os.Stderr, "error: --url is required")
		os.Exit(1)
	}
	if cfg.mode != "gateway" && cfg.mode != "ingest" {
		fmt.Fprintf(os.Stderr, "error: --mode must be 'gateway' or 'ingest', got %q\n", cfg.mode)
		os.Exit(1)
	}
	if cfg.streamRatio < 0 || cfg.streamRatio > 1 {
		fmt.Fprintln(os.Stderr, "error: --stream-ratio must be between 0.0 and 1.0")
		os.Exit(1)
	}

	// Build the tenant token list: --tokens takes precedence, fall back to --token.
	var tenants []tenantToken
	if tokensFlag != "" {
		for _, pair := range strings.Split(tokensFlag, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				fmt.Fprintf(os.Stderr, "error: --tokens entry %q must be tenant_id:token\n", pair)
				os.Exit(1)
			}
			tenants = append(tenants, tenantToken{tenantID: parts[0], token: parts[1]})
		}
		if len(tenants) == 0 {
			fmt.Fprintln(os.Stderr, "error: --tokens provided but no valid entries parsed")
			os.Exit(1)
		}
		fmt.Printf("Multi-tenant mode: %d tenants\n", len(tenants))
		for _, t := range tenants {
			fmt.Printf("  tenant %s\n", t.tenantID)
		}
	} else if cfg.token != "" {
		tenants = []tenantToken{{tenantID: "default", token: cfg.token}}
	}

	var counter atomic.Uint64

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.tlsSkipVerify},
			MaxIdleConnsPerHost: cfg.concurrency,
			MaxIdleConns:        cfg.concurrency * 2,
		},
		Timeout: 120 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()

	results := make(chan result, cfg.concurrency*100)
	var wg sync.WaitGroup

	var ticker *time.Ticker
	if cfg.rps > 0 {
		interval := time.Second / time.Duration(cfg.rps)
		if interval < 100*time.Microsecond {
			interval = 100 * time.Microsecond
		}
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
	}

	for i := 0; i < cfg.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(ctx, client, &cfg, tenants, &counter, results, ticker)
		}()
	}

	var collected []result
	done := make(chan struct{})
	go func() {
		for r := range results {
			collected = append(collected, r)
		}
		close(done)
	}()

	wg.Wait()
	close(results)
	<-done

	printReport(&cfg, collected)
}

func worker(ctx context.Context, client *http.Client, cfg *config, tenants []tenantToken, counter *atomic.Uint64, out chan<- result, ticker *time.Ticker) {
	for {
		if ctx.Err() != nil {
			return
		}
		if ticker != nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}

		var latency time.Duration
		var err error

		switch cfg.mode {
		case "gateway":
			// Pick the token for this request via round-robin across tenants.
			token := ""
			if len(tenants) > 0 {
				idx := counter.Add(1) - 1
				token = tenants[idx%uint64(len(tenants))].token
			}
			stream := rand.Float64() < cfg.streamRatio
			latency, err = GatewayRequest(ctx, client, cfg, token, stream)
		case "ingest":
			latency, err = IngestRequest(ctx, client, cfg.url)
		}

		r := result{latency: latency, err: err}
		if err == nil {
			r.statusCode = 200
		}

		select {
		case out <- r:
		case <-ctx.Done():
			return
		}
	}
}

func printReport(cfg *config, results []result) {
	if len(results) == 0 {
		fmt.Println("No requests completed.")
		return
	}

	var errors int
	latencies := make([]time.Duration, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			errors++
		} else {
			latencies = append(latencies, r.latency)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	count := len(results)
	rps := float64(count) / cfg.duration.Seconds()
	errPct := float64(errors) / float64(count) * 100

	fmt.Println("──────────────────────────────────")
	fmt.Printf("Mode:       %s\n", cfg.mode)
	fmt.Printf("Duration:   %s\n", cfg.duration)
	fmt.Printf("Workers:    %d\n", cfg.concurrency)
	fmt.Printf("Requests:   %d\n", count)
	fmt.Printf("Errors:     %d (%.1f%%)\n", errors, errPct)
	fmt.Printf("RPS:        %.1f\n", rps)
	if len(latencies) > 0 {
		fmt.Printf("Latency p50: %s\n", pctile(latencies, 0.50))
		fmt.Printf("Latency p95: %s\n", pctile(latencies, 0.95))
		fmt.Printf("Latency p99: %s\n", pctile(latencies, 0.99))
		fmt.Printf("Latency max: %s\n", latencies[len(latencies)-1])
	}
	fmt.Println("──────────────────────────────────")
}

func pctile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
