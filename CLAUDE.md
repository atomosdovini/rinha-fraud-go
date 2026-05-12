# Rinha de Backend 2026 — Go Fraud Detection API

## Goal
POST /fraud-score: receives card transaction JSON, returns approved (bool) and fraud_score (float).
Uses IVF vector search over 3M pre-labeled reference vectors.
GET /ready: health check, returns 200 when index is loaded.

## Architecture
nginx (round-robin, port 9999) → 2× Go API instances (port 8080)
Total budget: 1 CPU, 350MB RAM across all services.

## Key Design Decisions
- **int8 quantization**: 3M×14 float32 (168MB) → int8 (42MB) per instance
- **IVF index**: 256 clusters built at Docker build time; search probes top-6 clusters (~70K vectors)
- **fasthttp**: zero-allocation HTTP server for low p99 latency
- **go-json (goccy)**: faster JSON unmarshaling for nested request struct
- **scratch final image**: minimal attack surface, fast startup

## Vectorization Spec
See docs/en/DETECTION_RULES.md in the challenge repo.
The -1.0 sentinel (indices 5 & 6 when last_transaction=null) maps to int8(-127).
Never clamp or replace sentinel values — reference vectors with null history also use -1.

## Scoring Trade-offs
- Never return 5xx (weight=5 per error, worst penalty) — always return 200
- FN (missed fraud) penalized 3×; FP penalized 1× — bias toward denying
- p99 > 2000ms = -3000 hard floor; target p99 < 10ms

## Build
```
docker build -t rinha-fraud-go .     # 3-stage: compile → build-index → final
docker compose up
```

## Critical Files
- internal/vectorize/vectorize.go  — transaction → [14]float32
- internal/index/build.go          — k-means + quantize + binary serialization
- internal/index/index.go          — IVF load + search (hot path)
- internal/handler/fraud.go        — POST /fraud-score handler
- cmd/indexer/main.go              — build-time index builder (streams references.json.gz)
- cmd/api/main.go                  — API server entrypoint

## Resource Allocation
| Service | CPU   | Memory |
|---------|-------|--------|
| nginx   | 0.10  | 30MB   |
| api1    | 0.45  | 160MB  |
| api2    | 0.45  | 160MB  |
| Total   | 1.00  | 350MB  |

## Binary Index Format (data/index.bin)
Header: magic(4B) version(4B) num_clusters(4B) total_vectors(4B) dims(4B) scale(4B)
Centroids: [256 × 14 × 4B] float32
Per cluster: count(4B) + [count×14B] int8 vectors + [count×1B] labels
Total size: ~45MB

## Testing
```
# Unit tests (requires Go installed)
go test ./internal/vectorize/...

# Integration test (requires running stack)
curl http://localhost:9999/ready
curl -X POST http://localhost:9999/fraud-score \
  -H 'Content-Type: application/json' \
  -d @resources/example-payloads.json
```
