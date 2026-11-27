package main

import (
	"fmt"
	"math/big"
	"strings"
)

// Demand covers the whole pool. Bid lots describe one form in this business's forecast.
type bidForecast struct {
	ExpectedBidShares string `json:"expected_bid_shares,omitempty"`
	BidLots           string `json:"bid_lots,omitempty"`
}

type bidForecasts map[string]bidForecast

func (forecasts bidForecasts) normalize() error {
	for code, forecast := range forecasts {
		if code != "RETAIL" && code != "SHNI" && code != "BHNI" {
			return fmt.Errorf("unknown bid forecast category %q", code)
		}
		for _, input := range []string{forecast.ExpectedBidShares, forecast.BidLots} {
			if input != "" && !applicationCountPattern.MatchString(input) {
				return fmt.Errorf("%s forecast shares and lots must be whole numbers of up to 24 digits", code)
			}
		}
		if forecast.ExpectedBidShares != "" {
			forecast.ExpectedBidShares = strings.TrimLeft(forecast.ExpectedBidShares, "0")
			if forecast.ExpectedBidShares == "" {
				forecast.ExpectedBidShares = "0"
			}
		}
		if forecast.BidLots != "" {
			forecast.BidLots = strings.TrimLeft(forecast.BidLots, "0")
			if forecast.BidLots == "" {
				return fmt.Errorf("%s bid lots must be greater than zero", code)
			}
		}
		if forecast == (bidForecast{}) {
			delete(forecasts, code)
		} else {
			forecasts[code] = forecast
		}
	}
	return nil
}

func (a applicationForecasts) count(pool string) *string {
	switch pool {
	case "RETAIL", "RETAIL_199":
		return a.Retail
	case "SHNI":
		return a.SHNI
	case "BHNI":
		return a.BHNI
	case "SHAREHOLDER":
		return a.Shareholder
	}
	return nil
}

type allotmentEstimate struct {
	Method           string  `json:"method"`
	ReservedShares   string  `json:"reserved_shares"`
	ExpectedShares   *string `json:"expected_shares_per_form,omitempty"`
	AllotmentChance  *string `json:"allotment_chance,omitempty"`
	QuotaTransfer    string  `json:"quota_transfer_shares,omitempty"`
	sharesIfAllotted *big.Rat
	chance           *big.Rat
}

func (p allotmentPool) bidShares(lots int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(lots), big.NewInt(p.LotSize))
}

func (p allotmentPool) expectedDemand(input bidForecast, forms *string) (shares *big.Int, valid bool) {
	if input.ExpectedBidShares == "" {
		if forms != nil && p.MaxBidLots != nil && p.MinBidLots == *p.MaxBidLots {
			count, _ := new(big.Int).SetString(*forms, 10)
			return new(big.Int).Mul(count, p.bidShares(p.MinBidLots)), true
		}
		return nil, true
	}
	shares, _ = new(big.Int).SetString(input.ExpectedBidShares, 10)
	if forms == nil {
		return shares, true
	}
	count, _ := new(big.Int).SetString(*forms, 10)
	if shares.Cmp(new(big.Int).Mul(count, p.bidShares(p.MinBidLots))) < 0 {
		return nil, false
	}
	if p.MaxBidLots != nil && shares.Cmp(new(big.Int).Mul(count, p.bidShares(*p.MaxBidLots))) > 0 {
		return nil, false
	}
	return shares, true
}

// Use both demand forecasts before moving unused NII shares between the published sub-pools.
func transferNIIQuota(pools map[string]allotmentPool, forecasts bidForecasts, forms applicationForecasts) map[string]string {
	transfers := map[string]string{}
	small, large := pools["SHNI"], pools["BHNI"]
	if !small.transferEligible || !large.transferEligible {
		return transfers
	}
	smallDemand, _ := small.expectedDemand(forecasts["SHNI"], forms.SHNI)
	largeDemand, _ := large.expectedDemand(forecasts["BHNI"], forms.BHNI)
	if smallDemand == nil || largeDemand == nil {
		return transfers
	}
	smallQuota, _ := new(big.Int).SetString(small.ReservedShares, 10)
	largeQuota, _ := new(big.Int).SetString(large.ReservedShares, 10)
	smallSurplus := new(big.Int).Sub(smallQuota, smallDemand)
	largeSurplus := new(big.Int).Sub(largeQuota, largeDemand)
	move := new(big.Int)
	switch {
	case smallSurplus.Sign() > 0 && largeSurplus.Sign() < 0:
		move.Set(smallSurplus)
		if wanted := largeSurplus.Neg(largeSurplus); wanted.Cmp(move) < 0 {
			move.Set(wanted)
		}
	case largeSurplus.Sign() > 0 && smallSurplus.Sign() < 0:
		move.Set(largeSurplus)
		if wanted := smallSurplus.Neg(smallSurplus); wanted.Cmp(move) < 0 {
			move.Set(wanted)
		}
		move.Neg(move)
	default:
		return transfers
	}
	small.ReservedShares = smallQuota.Sub(smallQuota, move).String()
	large.ReservedShares = largeQuota.Add(largeQuota, move).String()
	pools["SHNI"], pools["BHNI"] = small, large
	transfers["BHNI"], transfers["SHNI"] = move.String(), move.Neg(move).String()
	return transfers
}

func estimatePool(pool allotmentPool, forms string, inputs bidForecast, transfer string) allotmentEstimate {
	result := allotmentEstimate{ReservedShares: pool.ReservedShares, QuotaTransfer: transfer}
	count, _ := new(big.Int).SetString(forms, 10)
	quota, _ := new(big.Int).SetString(pool.ReservedShares, 10)
	minimum := big.NewInt(pool.MinimumShares)
	neededForMinimums := new(big.Int).Mul(count, minimum)
	demand, valid := pool.expectedDemand(inputs, &forms)
	if !valid {
		result.Method = "pool_demand_invalid"
		return result
	}
	lots := big.NewInt(pool.MinBidLots)
	if pool.fixedBidLots != nil {
		lots.SetInt64(*pool.fixedBidLots)
	} else if inputs.BidLots != "" {
		lots.SetString(inputs.BidLots, 10)
	}
	maxLots := pool.MaxBidLots
	if lots.Cmp(big.NewInt(pool.MinBidLots)) < 0 || maxLots != nil && lots.Cmp(big.NewInt(*maxLots)) > 0 {
		result.Method = "pool_bid_invalid"
		return result
	}
	bid := new(big.Int).Mul(lots, big.NewInt(pool.LotSize))
	if demand != nil {
		// The market demand must cover this bid and the minimum bids on the other forms.
		otherMinimumBids := new(big.Int).Sub(count, big.NewInt(1))
		otherMinimumBids.Mul(otherMinimumBids, pool.bidShares(pool.MinBidLots))
		if new(big.Int).Add(otherMinimumBids, bid).Cmp(demand) > 0 {
			result.Method = "pool_demand_invalid"
			return result
		}
	}

	shareCount := new(big.Rat).SetInt(minimum)
	chance := big.NewRat(1, 1)
	switch {
	case neededForMinimums.Cmp(quota) > 0:
		result.Method = "lottery"
		slots, _ := new(big.Int).SetString(pool.basis().Capacity, 10)
		chance.SetFrac(slots, count)
	case demand != nil && demand.Cmp(quota) <= 0:
		result.Method = "full_bid"
		shareCount.SetInt(bid)
	case bid.Cmp(minimum) == 0 || quota.Cmp(neededForMinimums) == 0:
		result.Method = "minimum"
	case demand == nil:
		result.Method = "minimum_only"
	default:
		result.Method = "proportionate"
		surplus := new(big.Int).Sub(quota, neededForMinimums)
		excessDemand := new(big.Int).Sub(demand, neededForMinimums)
		excessBid := new(big.Int).Sub(bid, minimum)
		extra := new(big.Rat).SetFrac(new(big.Int).Mul(surplus, excessBid), excessDemand)
		if pool.sme {
			// Schedule XIV Part A2 allots additional SME NII shares in whole trading lots.
			denominator := new(big.Int).Mul(extra.Denom(), big.NewInt(pool.LotSize))
			lots := new(big.Int).Quo(extra.Num(), denominator)
			extra.SetInt(lots.Mul(lots, big.NewInt(pool.LotSize)))
		} else {
			extra.SetString(extra.FloatString(0))
		}
		shareCount.Add(shareCount, extra)
	}
	result.sharesIfAllotted, result.chance = shareCount, chance
	result.ExpectedShares = positionString(new(big.Rat).Mul(shareCount, chance), 6)
	result.AllotmentChance = positionString(chance, 6)
	return result
}
