package vectorize

import (
	"github.com/atomosdovini/rinha-fraud-go/internal/config"
)

// digits0 returns the int value of a decimal digit byte.
func digits0(b byte) int { return int(b - '0') }

// jdnToUnixDays converts a Julian Day Number to days since Unix epoch (1970-01-01).
const unixEpochJDN = 2440588

// dateToUnixDays returns days since 1970-01-01 using the proleptic Gregorian JDN formula.
func dateToUnixDays(year, month, day int) int64 {
	a := (14 - month) / 12
	y := year + 4800 - a
	m := month + 12*a - 3
	jdn := int64(day+(153*m+2)/5+365*y+y/4-y/100+y/400-32045) - unixEpochJDN
	return jdn
}

// tsMinutes converts an RFC3339 timestamp to minutes since Unix epoch (UTC).
// Returns (totalMins, hourUTC, weekday Mon=0..Sun=6, ok).
// Handles Z and ±hh:mm suffixes.
func tsMinutes(s string) (totalMins int64, hour, wd int, ok bool) {
	if len(s) < 19 {
		return
	}
	year := digits0(s[0])*1000 + digits0(s[1])*100 + digits0(s[2])*10 + digits0(s[3])
	month := digits0(s[5])*10 + digits0(s[6])
	day := digits0(s[8])*10 + digits0(s[9])
	h := digits0(s[11])*10 + digits0(s[12])
	m := digits0(s[14])*10 + digits0(s[15])

	// Timezone offset in minutes (subtract to get UTC).
	var tzOff int
	if len(s) > 19 && (s[19] == '+' || s[19] == '-') && len(s) >= 25 {
		tzH := digits0(s[20])*10 + digits0(s[21])
		tzM := digits0(s[23])*10 + digits0(s[24])
		tzOff = tzH*60 + tzM
		if s[19] == '-' {
			tzOff = -tzOff
		}
	}

	days := dateToUnixDays(year, month, day)
	totalMins = days*1440 + int64(h)*60 + int64(m) - int64(tzOff)

	// UTC minute-of-day → hour.
	mod := int(totalMins % 1440)
	if mod < 0 {
		mod += 1440
	}
	hour = mod / 60

	// Go weekday: 1970-01-01 was Thursday = 4.
	// (days + 4) % 7 gives 0=Sun,1=Mon,...,6=Sat.
	utcDays := totalMins / 1440
	if totalMins < 0 && totalMins%1440 != 0 {
		utcDays--
	}
	goWd := int((utcDays+4)%7 + 7) % 7 // ensure non-negative
	// Convert to Mon=0..Sun=6 as expected by the vectorizer.
	if goWd == 0 {
		wd = 6
	} else {
		wd = goWd - 1
	}
	ok = true
	return
}

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

	// 3: hour_of_day, 4: day_of_week — parsed without allocation.
	reqMins, reqHour, reqWd, reqOk := tsMinutes(p.Transaction.RequestedAt)
	if reqOk {
		vec[3] = float32(reqHour) / 23.0
		vec[4] = float32(reqWd) / 6.0
	}

	// 5: minutes_since_last_tx, 6: km_from_last_tx
	if p.LastTransaction != nil {
		if lastMins, _, _, lastOk := tsMinutes(p.LastTransaction.Timestamp); lastOk && reqOk {
			mins := float32(reqMins-lastMins)
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
