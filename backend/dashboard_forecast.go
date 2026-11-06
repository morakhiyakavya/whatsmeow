package main

import "math/big"

func forecastPositionBucket(p *livePosition, b *positionBucket, quantity, gmp *big.Rat) error {
	b.ExpectedShares = nil
	if b.Subject == "subject_2" {
		b.PremiumCost = nil
	}
	pool := b.Form
	if pool == "RETAIL_199" {
		pool = "RETAIL"
	}
	var expected *string
	switch pool {
	case "RETAIL":
		expected = p.Applications.Retail
	case "SHNI":
		expected = p.Applications.SHNI
	case "BHNI":
		expected = p.Applications.BHNI
	case "SHAREHOLDER":
		expected = p.Applications.Shareholder
	}
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
			ratio := new(big.Rat).Quo(capacity, total)
			b.ExpectedShares = positionString(new(big.Rat).Mul(quantity, new(big.Rat).Mul(q, ratio)), 6)
			b.Shares = b.ExpectedShares
			if b.Subject == "subject_2" {
				amount, err := positionNumber(b.AgreedAmount)
				if err != nil {
					return err
				}
				b.PremiumCost = positionString(new(big.Rat).Mul(amount, ratio), 2)
			}
			if gmp != nil {
				b.Subject1Rate = positionString(new(big.Rat).Mul(new(big.Rat).Mul(q, gmp), ratio), 0)
				factor, err := positionNumber(p.Factor)
				if err != nil {
					return err
				}
				b.Subject2Rate = positionString(new(big.Rat).Mul(new(big.Rat).Mul(q, gmp), factor), 0)
			}
			return nil
		}
		reason = "forecast_outside_model"
	} else if ok {
		reason = "forecast_missing"
	}
	b.Reasons = append(b.Reasons, positionReason{"net_expected_shares", reason})
	if b.Subject == "subject_2" {
		b.Reasons = append(b.Reasons, positionReason{"net_premium_cost", reason})
	}
	return nil
}
