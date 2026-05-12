package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"github.com/valyala/fasthttp"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
	"github.com/atomosdovini/rinha-fraud-go/internal/handler"
	"github.com/atomosdovini/rinha-fraud-go/internal/index"
)

func main() {
	// Reduce GC frequency under load; leave GOMAXPROCS at default
	debug.SetGCPercent(400)

	indexPath := envOr("INDEX_PATH", "/data/index.bin")
	mccPath := envOr("MCC_PATH", "/resources/mcc_risk.json")
	normPath := envOr("NORM_PATH", "/resources/normalization.json")
	port := envOr("PORT", "8080")
	nprobe := 4 // probe 4 of 256 clusters (~47K vectors)

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
	log.Printf("index loaded — ready to serve on :%s", port)

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
		ReadBufferSize:  8192,
		WriteBufferSize: 4096,
		Concurrency:     512,
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
