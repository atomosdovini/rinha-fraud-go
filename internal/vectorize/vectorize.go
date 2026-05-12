package vectorize

import (
	"time"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
)

type Transaction struct {
	Amount      float32 `json:"amount"`
	Installments int32  `json:"installments"`
	RequestedAt string  `json:"requested_at"`
}

type Customer struct {
	AvgAmount       float32  `json:"avg_amount"`
	TxCount24h      int32    `json:"tx_count_24h"`
	KnownMerchants  []string `json:"known_merchants"`
}

type Merchant struct {
	ID        string  `json:"id"`
	MCC       string  `json:"mcc"`
	AvgAmount float32 `json:"avg_amount"`
}

type Terminal struct {
	IsOnline    bool    `json:"is_online"`
	CardPresent bool    `json:"card_present"`
	KmFromHome  float32 `json:"km_from_home"`
}

type LastTransaction struct {
	Timestamp      string  `json:"timestamp"`
	KmFromCurrent  float32 `json:"km_from_current"`
}

type Payload struct {
	ID              string           `json:"id"`
	Transaction     Transaction      `json:"transaction"`
	Customer        Customer         `json:"customer"`
	Merchant        Merchant         `json:"merchant"`
	Terminal        Terminal         `json:"terminal"`
	LastTransaction *LastTransaction `json:"last_transaction"`
}

func clamp(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func Vectorize(p *Payload, cfg *config.Config) [14]float32 {
	var vec [14]float32
	n := &cfg.Norm

	// 0: amount
	vec[0] = clamp(p.Transaction.Amount / n.MaxAmount)

	// 1: installments
	vec[1] = clamp(float32(p.Transaction.Installments) / n.MaxInstallments)

	// 2: amount_vs_avg
	if p.Customer.AvgAmount > 0 {
		vec[2] = clamp((p.Transaction.Amount / p.Customer.AvgAmount) / n.AmountVsAvgRatio)
	}

	// 3: hour_of_day, 4: day_of_week
	if t, err := time.Parse(time.RFC3339, p.Transaction.RequestedAt); err == nil {
		t = t.UTC()
		vec[3] = float32(t.Hour()) / 23.0
		// weekday: Mon=0 .. Sun=6
		wd := int(t.Weekday()) // Sun=0 in Go
		if wd == 0 {
			wd = 6
		} else {
			wd--
		}
		vec[4] = float32(wd) / 6.0
	}

	// 5: minutes_since_last_tx, 6: km_from_last_tx
	if p.LastTransaction != nil {
		if lastT, err := time.Parse(time.RFC3339, p.LastTransaction.Timestamp); err == nil {
			reqT, _ := time.Parse(time.RFC3339, p.Transaction.RequestedAt)
			mins := float32(reqT.Sub(lastT).Minutes())
			if mins < 0 {
				mins = -mins
			}
			vec[5] = clamp(mins / n.MaxMinutes)
		}
		vec[6] = clamp(p.LastTransaction.KmFromCurrent / n.MaxKm)
	} else {
		vec[5] = -1
		vec[6] = -1
	}

	// 7: km_from_home
	vec[7] = clamp(p.Terminal.KmFromHome / n.MaxKm)

	// 8: tx_count_24h
	vec[8] = clamp(float32(p.Customer.TxCount24h) / n.MaxTxCount24h)

	// 9: is_online
	if p.Terminal.IsOnline {
		vec[9] = 1
	}

	// 10: card_present
	if p.Terminal.CardPresent {
		vec[10] = 1
	}

	// 11: unknown_merchant (1 if NOT in known_merchants)
	vec[11] = 1
	for _, m := range p.Customer.KnownMerchants {
		if m == p.Merchant.ID {
			vec[11] = 0
			break
		}
	}

	// 12: mcc_risk
	vec[12] = cfg.MCCRiskScore(p.Merchant.MCC)

	// 13: merchant_avg_amount
	vec[13] = clamp(p.Merchant.AvgAmount / n.MaxMerchantAvgAmount)

	return vec
}
