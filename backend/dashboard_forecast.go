package main

import "math/big"

func forecastPositionBucket(p *livePosition, b *positionBucket, quantity, gmp *big.Rat) error {
	b.ExpectedShares = nil
	if b.Subject == "subject_2" {
		b.PremiumCost = nil
	}
	if b.Form == "SHAREHOLDER" && p.ShareholderForecast != nil {
		return forecastShareholderBucket(p, b, quantity, gmp)
	}
	if estimate, ok := p.AllotmentEstimates[b.Form]; ok {
		if estimate.chance == nil {
			recordForecastUnavailable(b, estimate.Method)
			return nil
		}
		if estimate.Method == "minimum_only" {
			b.Reasons = append(b.Reasons, positionReason{"net_expected_shares", "minimum_allotment_only"})
		}
		return applyPositionForecast(p, b, quantity, gmp, estimate.sharesIfAllotted, estimate.chance)
	}
	expected := p.Applications.count(b.Form)
	basis, ok := p.Basis[b.Form]
	reason := "allotment_model_unavailable"
	if ok && expected != nil {
		capacity, err := positionNumber(basis.Capacity)
		if err != nil {
			return err
		}
		total, err := positionNumber(*expected)
		if err != nil {
			return err
		}
		q, err := positionNumber(basis.Shares)
		if err != nil {
			return err
		}
		if capacity.Sign() > 0 && total.Sign() > 0 && q.Sign() > 0 && capacity.Cmp(total) <= 0 {
			return applyPositionForecast(p, b, quantity, gmp, q, new(big.Rat).Quo(capacity, total))
		}
		reason = "forecast_outside_model"
	} else if ok {
		reason = "forecast_missing"
	}
	recordForecastUnavailable(b, reason)
	return nil
}

func recordForecastUnavailable(b *positionBucket, reason string) {
	b.Reasons = append(b.Reasons, positionReason{"net_expected_shares", reason})
	if b.Subject == "subject_2" {
		b.Reasons = append(b.Reasons, positionReason{"net_premium_cost", reason})
	}
}

func applyPositionForecast(p *livePosition, b *positionBucket, quantity, gmp, sharesIfAllotted, chance *big.Rat) error {
	expected := new(big.Rat).Mul(sharesIfAllotted, chance)
	b.ExpectedShares = positionString(new(big.Rat).Mul(quantity, expected), 6)
	b.Shares = b.ExpectedShares
	if b.Subject == "subject_2" {
		amount, err := positionNumber(b.AgreedAmount)
		if err != nil {
			return err
		}
		b.PremiumCost = positionString(new(big.Rat).Mul(amount, chance), 2)
	}
	if gmp != nil {
		b.Subject1Rate = positionString(new(big.Rat).Mul(expected, gmp), 0)
		factor, err := positionNumber(p.Factor)
		if err != nil {
			return err
		}
		b.Subject2Rate = positionString(new(big.Rat).Mul(new(big.Rat).Mul(sharesIfAllotted, gmp), factor), 0)
	}
	return nil
}
