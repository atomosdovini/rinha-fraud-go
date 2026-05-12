package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/valyala/fasthttp"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
	"github.com/atomosdovini/rinha-fraud-go/internal/handler"
	"github.com/atomosdovini/rinha-fraud-go/internal/index"
)

func main() {
	// Single OS thread — we're CPU-bound on one core (0.45 CPU limit).
	// Eliminates goroutine scheduler overhead and false sharing.
	runtime.GOMAXPROCS(1)

	indexPath := envOr("INDEX_PATH", "/data/index.bin")
	mccPath := envOr("MCC_PATH", "/resources/mcc_risk.json")
	normPath := envOr("NORM_PATH", "/resources/normalization.json")
	port := envOr("PORT", "8080")
	nprobe := 12 // fast probe; SearchAll (bbox-pruned full scan) used for ambiguous results

	log.Printf("loading config from %s and %s", normPath, mccPath)
	cfg, err := config.Load(normPath, mccPath)
	if err != nil {
		log.Fatalf("config load error: %v", err)
	}

	log.Printf("loading index from %s", indexPath)
	idx, err := index.Load(indexPath)
	if err != nil {
		log.Fatalf("index load error: %v", err)
	}

	log.Printf("warming up index...")
	idx.Warmup()

	// Cap heap to 150 MB so GC stays off our working set, then disable GC entirely.
	// SetMemoryLimit prevents any surprise heap growth before SetGCPercent kicks in.
	debug.SetMemoryLimit(150 * 1024 * 1024)
	debug.SetGCPercent(-1)

	log.Printf("index ready — serving on :%s", port)

	readyH := handler.NewReadyHandler(idx.IsReady)
	fraudH := handler.NewFraudHandler(idx, cfg, nprobe)

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			switch string(ctx.Path()) {
			case "/ready":
				readyH.Handle(ctx)
			case "/fraud-score":
				fraudH.Handle(ctx)
			default:
				ctx.SetStatusCode(fasthttp.StatusNotFound)
			}
		},
		ReadBufferSize:                  1024,
		WriteBufferSize:                 1024,
		Concurrency:                     4096,
		DisableHeaderNamesNormalizing:   true,
		NoDefaultDate:                   true,
		NoDefaultServerHeader:           true,
		NoDefaultContentType:            true,
		DisablePreParseMultipartForm:    true,
	}

	if err := server.ListenAndServe(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
