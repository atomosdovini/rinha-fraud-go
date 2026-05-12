package config

import (
	"encoding/json"
	"os"
)

type Normalization struct {
	MaxAmount           float32 `json:"max_amount"`
	MaxInstallments     float32 `json:"max_installments"`
	AmountVsAvgRatio    float32 `json:"amount_vs_avg_ratio"`
	MaxMinutes          float32 `json:"max_minutes"`
	MaxKm               float32 `json:"max_km"`
	MaxTxCount24h       float32 `json:"max_tx_count_24h"`
	MaxMerchantAvgAmount float32 `json:"max_merchant_avg_amount"`
}

type Config struct {
	Norm    Normalization
	MCCRisk map[string]float32
}

func Load(normPath, mccPath string) (*Config, error) {
	normData, err := os.ReadFile(normPath)
	if err != nil {
		return nil, err
	}
	var norm Normalization
	if err := json.Unmarshal(normData, &norm); err != nil {
		return nil, err
	}

	mccData, err := os.ReadFile(mccPath)
	if err != nil {
		return nil, err
	}
	var mccRisk map[string]float32
	if err := json.Unmarshal(mccData, &mccRisk); err != nil {
		return nil, err
	}

	return &Config{Norm: norm, MCCRisk: mccRisk}, nil
}

func (c *Config) MCCRiskScore(mcc string) float32 {
	if v, ok := c.MCCRisk[mcc]; ok {
		return v
	}
	return 0.5
}
