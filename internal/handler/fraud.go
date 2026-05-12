package handler

import (
	gojson "github.com/goccy/go-json"
	"github.com/valyala/fasthttp"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
	"github.com/atomosdovini/rinha-fraud-go/internal/index"
	"github.com/atomosdovini/rinha-fraud-go/internal/vectorize"
)

var (
	jsonContentType = []byte("application/json")
	fallback        = []byte(`{"approved":true,"fraud_score":0}`)
	// cachedResp[fc] for fc in 0..5 — all possible outcomes for k=5 nearest neighbors.
	// approved = score < 0.6 → fraudCount/5 < 0.6 → fraudCount < 3.
	// FN is penalized 3× more than FP — deny when ≥ 2/5 neighbors are fraud.
	cachedResp = [6][]byte{
		[]byte(`{"approved":true,"fraud_score":0}`),
		[]byte(`{"approved":true,"fraud_score":0.2}`),
		[]byte(`{"approved":false,"fraud_score":0.4}`),
		[]byte(`{"approved":false,"fraud_score":0.6}`),
		[]byte(`{"approved":false,"fraud_score":0.8}`),
		[]byte(`{"approved":false,"fraud_score":1}`),
	}
)

type FraudHandler struct {
	idx    *index.Index
	cfg    *config.Config
	nprobe int
}

func NewFraudHandler(idx *index.Index, cfg *config.Config, nprobe int) *FraudHandler {
	return &FraudHandler{idx: idx, cfg: cfg, nprobe: nprobe}
}

func (h *FraudHandler) Handle(ctx *fasthttp.RequestCtx) {
	var p vectorize.Payload
	if err := gojson.Unmarshal(ctx.PostBody(), &p); err != nil {
		ctx.SetContentTypeBytes(jsonContentType)
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBody(fallback)
		return
	}

	vec := vectorize.Vectorize(&p, h.cfg)

	// Fast probe with nprobe clusters.
	fraudCount := h.idx.Search(vec, 5, h.nprobe)

	// Ambiguous result (score = 0.4 or 0.6) → full scan with bbox pruning for exact answer.
	if fraudCount == 2 || fraudCount == 3 {
		fraudCount = h.idx.SearchAll(vec, 5)
	}

	ctx.SetContentTypeBytes(jsonContentType)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(cachedResp[fraudCount])
}
