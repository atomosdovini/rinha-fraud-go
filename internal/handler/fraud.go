package handler

import (
	"strconv"

	gojson "github.com/goccy/go-json"
	"github.com/valyala/fasthttp"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
	"github.com/atomosdovini/rinha-fraud-go/internal/index"
	"github.com/atomosdovini/rinha-fraud-go/internal/vectorize"
)

var (
	jsonContentType = []byte("application/json")
	respApproved    = []byte(`{"approved":true,"fraud_score":`)
	respDenied      = []byte(`{"approved":false,"fraud_score":`)
	respSuffix      = []byte("}")
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
		// never return 5xx — fall back to safe response
		writeResponse(ctx, true, 0.0)
		return
	}

	vec := vectorize.Vectorize(&p, h.cfg)
	fraudCount := h.idx.Search(vec, 5, h.nprobe)
	fraudScore := float64(fraudCount) / 5.0
	approved := fraudScore < 0.6

	writeResponse(ctx, approved, fraudScore)
}

func writeResponse(ctx *fasthttp.RequestCtx, approved bool, score float64) {
	ctx.SetContentTypeBytes(jsonContentType)
	ctx.SetStatusCode(fasthttp.StatusOK)

	var buf []byte
	if approved {
		buf = append(buf, respApproved...)
	} else {
		buf = append(buf, respDenied...)
	}
	buf = strconv.AppendFloat(buf, score, 'f', -1, 64)
	buf = append(buf, respSuffix...)
	ctx.SetBody(buf)
}
