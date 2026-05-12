package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/atomosdovini/rinha-fraud-go/internal/index"
)

func main() {
	refs := flag.String("refs", "resources/references.json.gz", "path to references.json.gz")
	out := flag.String("out", "data/index.bin", "output binary index path")
	clusters := flag.Int("clusters", 256, "number of IVF clusters")
	iters := flag.Int("iters", 20, "number of k-means iterations")
	flag.Parse()

	// ensure output dir exists
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}

	fmt.Printf("loading vectors from %s\n", *refs)
	start := time.Now()
	records, err := index.LoadAllVectors(*refs)
	if err != nil {
		log.Fatalf("load vectors: %v", err)
	}
	fmt.Printf("loaded %d vectors in %v\n", len(records), time.Since(start))

	fmt.Printf("building IVF index with %d clusters...\n", *clusters)
	start = time.Now()
	if err := index.BuildIndex(records, *out, *clusters, *iters); err != nil {
		log.Fatalf("build index: %v", err)
	}
	fmt.Printf("index written to %s in %v\n", *out, time.Since(start))

	fi, _ := os.Stat(*out)
	fmt.Printf("index size: %.1f MB\n", float64(fi.Size())/1024/1024)
}
