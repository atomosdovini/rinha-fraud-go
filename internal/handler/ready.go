package handler

import "github.com/valyala/fasthttp"

type ReadyHandler struct {
	isReady func() bool
}

func NewReadyHandler(isReady func() bool) *ReadyHandler {
	return &ReadyHandler{isReady: isReady}
}

func (h *ReadyHandler) Handle(ctx *fasthttp.RequestCtx) {
	if h.isReady() {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("ok")
	} else {
		ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	}
}
