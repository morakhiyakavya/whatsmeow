package main

import (
	"errors"
	"math/big"
	"strings"
)

type shareholderForecast struct {
	ExpectedBidShares string `json:"expected_bid_shares"`
	SharesPerBid      string `json:"shares_per_bid"`
}

// Read the published reservation even when the IPO has no subscription rows or price band yet.
const shareholderQuotaSQL = `(SELECT min(r.reserved_shares)::text FROM ipo_pos.ipo_reservations r
 WHERE r.ipo_id=i.id AND r.reserved_shares>0 AND lower(r.category) ~ 'share[[:space:]_-]*holder'
 HAVING count(DISTINCT r.reserved_shares)=1)`

func (f *shareholderForecast) normalize() error {
	if f == nil || *f == (shareholderForecast{}) {
		return nil
	}
	for _, shares := range []*string{&f.ExpectedBidShares, &f.SharesPerBid} {
		if !applicationCountPattern.MatchString(*shares) {
			return errors.New("enter both shareholder share counts as positive whole numbers of up to 24 digits")
		}
		*shares = strings.TrimLeft(*shares, "0")
		if *shares == "" {
			return errors.New("shareholder share counts must be greater than zero")
		}
	}
	total, _ := new(big.Int).SetString(f.ExpectedBidShares, 10)
	bid, _ := new(big.Int).SetString(f.SharesPerBid, 10)
	if bid.Cmp(total) > 0 {
		return errors.New("expected total shareholder shares bid must include your bid")
	}
	return nil
}

func (f *shareholderForecast) estimate(quota string) (shares *big.Rat, fullBid bool) {
	if f == nil || quota == "" {
		return nil, false
	}
	reserved, _ := new(big.Rat).SetString(quota)
	total, _ := new(big.Rat).SetString(f.ExpectedBidShares)
	bid, _ := new(big.Rat).SetString(f.SharesPerBid)
	if reserved.Cmp(total) >= 0 {
		return bid, true
	}
	// shortcut: this is the pro-rata average before the registrar applies minimum allotment and rounding.
	return bid.Mul(bid, reserved.Quo(reserved, total)), false
}

func forecastShareholderBucket(p *livePosition, b *positionBucket, quantity, gmp *big.Rat) error {
	perBid, fullBid := p.ShareholderForecast.estimate(p.ShareholderReservedShares)
	if perBid == nil {
		b.Reasons = append(b.Reasons, positionReason{"net_expected_shares", "shareholder_quota_missing"})
		if b.Subject == "subject_2" {
			b.Reasons = append(b.Reasons, positionReason{"net_premium_cost", "shareholder_quota_missing"})
		}
		return nil
	}
	b.ExpectedShares = positionString(new(big.Rat).Mul(quantity, perBid), 6)
	b.Shares = b.ExpectedShares
	if gmp != nil {
		b.Subject1Rate = positionString(new(big.Rat).Mul(perBid, gmp), 0)
	}
	if !fullBid {
		// Subject 2 is payable on a successful form. Share fill does not measure that probability.
		if b.Subject == "subject_2" {
			b.Reasons = append(b.Reasons, positionReason{"net_premium_cost", "shareholder_success_rate_unavailable"})
		}
		return nil
	}
	if b.Subject == "subject_2" {
		amount, err := positionNumber(b.AgreedAmount)
		if err != nil {
			return err
		}
		b.PremiumCost = positionString(amount, 2)
	}
	if gmp != nil {
		factor, err := positionNumber(p.Factor)
		if err != nil {
			return err
		}
		b.Subject2Rate = positionString(new(big.Rat).Mul(perBid, new(big.Rat).Mul(gmp, factor)), 0)
	}
	return nil
}
