package vectorize

import (
	"math"
	"testing"

	"github.com/atomosdovini/rinha-fraud-go/internal/config"
)

func defaultConfig() *config.Config {
	return &config.Config{
		Norm: config.Normalization{
			MaxAmount:            10000,
			MaxInstallments:      12,
			AmountVsAvgRatio:     10,
			MaxMinutes:           1440,
			MaxKm:                1000,
			MaxTxCount24h:        20,
			MaxMerchantAvgAmount: 10000,
		},
		MCCRisk: map[string]float32{
			"5411": 0.15,
			"5812": 0.30,
			"5912": 0.20,
			"7801": 0.80,
			"7802": 0.75,
			"7995": 0.85,
			"5311": 0.25,
			"5999": 0.50,
		},
	}
}

func approxEq(a, b, tol float32) bool {
	return math.Abs(float64(a-b)) < float64(tol)
}

// Legit transaction tx-1329056812 — expected vector from DETECTION_RULES.md
func TestVectorize_Legit(t *testing.T) {
	cfg := defaultConfig()
	p := &Payload{
		ID: "tx-1329056812",
		Transaction: Transaction{Amount: 41.12, Installments: 2, RequestedAt: "2026-03-11T18:45:53Z"},
		Customer:    Customer{AvgAmount: 82.24, TxCount24h: 3, KnownMerchants: []string{"MERC-003", "MERC-016"}},
		Merchant:    Merchant{ID: "MERC-016", MCC: "5411", AvgAmount: 60.25},
		Terminal:    Terminal{IsOnline: false, CardPresent: true, KmFromHome: 29.2331036248},
		LastTransaction: nil,
	}

	expected := [14]float32{0.0041, 0.1667, 0.05, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.006}
	got := Vectorize(p, cfg)

	for i, exp := range expected {
		if !approxEq(got[i], exp, 0.001) {
			t.Errorf("dim %d: expected %f, got %f", i, exp, got[i])
		}
	}
}

// Fraud transaction tx-3330991687 — expected vector from DETECTION_RULES.md
func TestVectorize_Fraud(t *testing.T) {
	cfg := defaultConfig()
	p := &Payload{
		ID: "tx-3330991687",
		Transaction: Transaction{Amount: 9505.97, Installments: 10, RequestedAt: "2026-03-14T05:15:12Z"},
		Customer:    Customer{AvgAmount: 81.28, TxCount24h: 20, KnownMerchants: []string{"MERC-008", "MERC-007", "MERC-005"}},
		Merchant:    Merchant{ID: "MERC-068", MCC: "7802", AvgAmount: 54.86},
		Terminal:    Terminal{IsOnline: false, CardPresent: true, KmFromHome: 952.2745933273},
		LastTransaction: nil,
	}

	expected := [14]float32{0.9506, 0.8333, 1.0, 0.2174, 0.8333, -1, -1, 0.9523, 1.0, 0, 1, 1, 0.75, 0.0055}
	got := Vectorize(p, cfg)

	for i, exp := range expected {
		if !approxEq(got[i], exp, 0.001) {
			t.Errorf("dim %d: expected %f, got %f", i, exp, got[i])
		}
	}
}

// Transaction with last_transaction present
func TestVectorize_WithLastTx(t *testing.T) {
	cfg := defaultConfig()
	p := &Payload{
		ID: "tx-3576980410",
		Transaction: Transaction{Amount: 384.88, Installments: 3, RequestedAt: "2026-03-11T20:23:35Z"},
		Customer:    Customer{AvgAmount: 769.76, TxCount24h: 3, KnownMerchants: []string{"MERC-009", "MERC-001"}},
		Merchant:    Merchant{ID: "MERC-001", MCC: "5912", AvgAmount: 298.95},
		Terminal:    Terminal{IsOnline: false, CardPresent: true, KmFromHome: 13.7090520965},
		LastTransaction: &LastTransaction{Timestamp: "2026-03-11T14:58:35Z", KmFromCurrent: 18.8626479774},
	}

	got := Vectorize(p, cfg)

	// indices 5 and 6 must NOT be -1
	if got[5] == -1 || got[6] == -1 {
		t.Error("indices 5 and 6 should not be -1 when last_transaction is present")
	}
	// minutes: 20:23:35 - 14:58:35 = 5h25m = 325 min; 325/1440 ≈ 0.2257
	if !approxEq(got[5], 0.2257, 0.01) {
		t.Errorf("dim 5 minutes_since: expected ~0.2257, got %f", got[5])
	}
	// km: 18.86/1000 = 0.01886
	if !approxEq(got[6], 0.0189, 0.001) {
		t.Errorf("dim 6 km_from_last: expected ~0.0189, got %f", got[6])
	}
}
