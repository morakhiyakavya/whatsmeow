package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ipoSubscription struct {
	Category, Label                      string
	Offered, Bid, Applications, Multiple *string
}
type ipoReservation struct {
	Category, Basis    string
	Shares, Percentage *string
}
type ipoShareholding struct {
	Holder    string
	Pre, Post *string
}
type ipoLockin struct{ Label, Date string }
type ipoSourceIssue struct {
	Symbol, Name, Board             string
	SourceStatus                    *string
	Open, Close, Listing, Low, High *string
	Lot                             int64
	Registrar, IssueText            string
	IssueShares, IssueAmount        *string
	Subscriptions                   []ipoSubscription
	Reservations                    []ipoReservation
	Shareholdings                   []ipoShareholding
	Lockins                         []ipoLockin
	Timeline                        map[string]any
	EnrichmentURL                   string
	DetailOK, EnrichmentOK          bool
}
type ipoSourceClient struct{ http *http.Client }

func newIPOSourceClient() *ipoSourceClient {
	return &ipoSourceClient{http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Hostname() != via[0].URL.Hostname() {
			return errors.New("source redirect rejected")
		}
		return nil
	}}}
}

func (s *ipoSourceClient) get(ctx context.Context, target string) ([]byte, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || (u.Host != "www.nseindia.com" && u.Host != "ipowatch.in") {
		return nil, errors.New("invalid source host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WhatsAppPOS-IPO-Catalogue/1.0")
	req.Header.Set("Accept", "application/json,text/html")
	res, err := s.http.Do(req)
	if err != nil {
		return nil, errors.New("source request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("source HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, errors.New("invalid source body size")
	}
	return data, nil
}

var sourceSymbolPattern = regexp.MustCompile(`^[A-Za-z0-9&_-]{1,32}$`)
var sourceNumericPattern = regexp.MustCompile(`[0-9][0-9,]*(?:\.[0-9]+)?`)
var sourceDecimalPattern = regexp.MustCompile(`^[0-9]{1,24}(?:\.[0-9]{1,18})?(?:[eE][+-]?[0-9]{1,2})?$`)
var expectedDatePattern = regexp.MustCompile(`(?i)^(?:[a-z]{3,9}(?:[-/][a-z]{3,9})?[ /-]+20[0-9]{2}|(?:q[1-4]|h[12])\s*20[0-9]{2}|20[0-9]{2}|tba|tbd|(?:expected|tentative)\b.{0,100})$`)

func sourceText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(strings.Trim(s, `"`))
	}
	return string(raw)
}

func sourceNumber(text string, integer bool) *string {
	text = strings.ReplaceAll(strings.TrimSpace(text), ",", "")
	if !sourceDecimalPattern.MatchString(text) {
		return nil
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || n.Sign() < 0 || n.Cmp(new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))) >= 0 {
		return nil
	}
	if integer && !n.IsInt() {
		return nil
	}
	value := n.FloatString(6)
	if integer {
		value = n.Num().String()
	}
	return &value
}

func sourceDate(text string) *string {
	text = strings.TrimSpace(text)
	for _, layout := range []string{"2006-01-02", "2-Jan-2006", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "2 Jan 2006"} {
		candidate := text
		if layout == "2-Jan-2006" {
			parts := strings.Split(text, "-")
			if len(parts) == 3 {
				candidate = parts[0] + "-" + strings.ToUpper(parts[1][:min(1, len(parts[1]))]) + strings.ToLower(parts[1][min(1, len(parts[1])):]) + "-" + parts[2]
			}
		}
		if d, err := time.Parse(layout, candidate); err == nil {
			value := d.Format("2006-01-02")
			return &value
		}
	}
	return nil
}

func sourceRows(data []byte) ([]map[string]json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rows); err == nil && rows != nil {
		return rows, nil
	}
	var wrapped struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil || wrapped.Data == nil {
		return nil, errors.New("source schema changed")
	}
	return wrapped.Data, nil
}

func parseIPOFeed(data []byte, past bool, now time.Time) ([]ipoSourceIssue, error) {
	rows, err := sourceRows(data)
	if err != nil {
		return nil, err
	}
	out := []ipoSourceIssue{}
	for _, row := range rows {
		name, open, close, band, series := sourceText(row["companyName"]), sourceText(row["issueStartDate"]), sourceText(row["issueEndDate"]), sourceText(row["issuePrice"]), sourceText(row["series"])
		if past {
			name, open, close, band, series = sourceText(row["company"]), sourceText(row["ipoStartDate"]), sourceText(row["ipoEndDate"]), sourceText(row["priceRange"]), sourceText(row["securityType"])
			if series != "EQ" && series != "SME" && series != "BE" {
				continue
			}
		}
		symbol := sourceText(row["symbol"])
		if !sourceSymbolPattern.MatchString(symbol) || !validText(name, 256) {
			return nil, errors.New("invalid IPO identity")
		}
		board := "Mainboard"
		if strings.EqualFold(series, "SME") {
			board = "SME"
		} else if series != "EQ" && series != "BE" {
			return nil, errors.New("unknown IPO board")
		}
		item := ipoSourceIssue{Symbol: symbol, Name: name, Board: board, Open: sourceDate(open), Close: sourceDate(close), Timeline: map[string]any{}}
		for _, date := range []struct {
			key, value string
			parsed     *string
		}{{"opening", open, item.Open}, {"closing", close, item.Close}} {
			if date.value != "" && date.value != "-" && date.parsed == nil {
				if !expectedDatePattern.MatchString(date.value) {
					return nil, errors.New("invalid IPO dates")
				}
				item.Timeline[date.key] = map[string]string{"value": date.value, "precision": "text", "status": "expected", "source_url": "https://www.nseindia.com/market-data/all-upcoming-issues-ipo"}
			}
		}
		if status := strings.ToLower(sourceText(row["status"])); status == "active" || status == "postponed" || status == "withdrawn" {
			item.SourceStatus = &status
		}
		if item.Open != nil && item.Close != nil && *item.Close < *item.Open {
			return nil, errors.New("inverted IPO dates")
		}
		if past && item.Close != nil && *item.Close < now.AddDate(0, 0, -120).Format("2006-01-02") {
			continue
		}
		item.Listing = sourceDate(sourceText(row["listingDate"]))
		prices := sourceNumericPattern.FindAllString(band, -1)
		if len(prices) > 0 {
			item.Low = sourceNumber(prices[0], false)
			item.High = item.Low
			if len(prices) > 1 {
				item.High = sourceNumber(prices[1], false)
			}
		}
		if len(prices) > 2 || (len(prices) > 0 && (item.Low == nil || item.High == nil)) {
			return nil, errors.New("invalid IPO price band")
		}
		if item.Low != nil {
			low, _ := new(big.Rat).SetString(*item.Low)
			high, _ := new(big.Rat).SetString(*item.High)
			if low.Cmp(high) > 0 {
				return nil, errors.New("inverted price band")
			}
		}
		if m := sourceNumber(sourceText(row["noOfTime"]), false); m != nil {
			item.Subscriptions = []ipoSubscription{{Category: "total", Label: "Total", Multiple: m, Offered: sourceNumber(sourceText(row["noOfSharesOffered"]), true), Bid: sourceNumber(sourceText(row["noOfsharesBid"]), true)}}
		}
		// NSE list issueSize can exclude anchors; preserve the raw label, not a fabricated total.
		if raw := sourceText(row["issueSize"]); raw != "" {
			item.IssueText = "NSE list issueSize: " + raw + " (source coverage; total issue not confirmed)"
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *ipoSourceClient) collect(ctx context.Context) ([]ipoSourceIssue, map[string]string) {
	results := map[string]string{"coverage": "NSE only; BSE-only coverage unverified"}
	bySymbol := map[string]ipoSourceIssue{}
	for _, feed := range []struct {
		key, path string
		past      bool
	}{{"past", "/api/public-past-issues", true}, {"upcoming", "/api/all-upcoming-issues?category=ipo", false}, {"current", "/api/ipo-current-issue", false}} {
		data, err := s.get(ctx, "https://www.nseindia.com"+feed.path)
		var rows []ipoSourceIssue
		if err == nil {
			rows, err = parseIPOFeed(data, feed.past, time.Now())
		}
		if err != nil {
			results[feed.key] = "failed"
			continue
		}
		results[feed.key] = "ok"
		for _, item := range rows {
			if previous, ok := bySymbol[item.Symbol]; ok {
				if item.Listing == nil {
					item.Listing = previous.Listing
				}
				if item.Low == nil {
					item.Low, item.High = previous.Low, previous.High
				}
			}
			bySymbol[item.Symbol] = item
		}
	}
	items := make([]ipoSourceIssue, 0, len(bySymbol))
	for _, item := range bySymbol {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Symbol < items[j].Symbol })
	today := time.Now().In(time.FixedZone("IST", 19800))
	for i := range items {
		item := &items[i]
		if item.Close != nil && *item.Close < today.AddDate(0, 0, -7).Format("2006-01-02") {
			continue
		}
		if item.Open != nil && *item.Open > today.AddDate(0, 0, 21).Format("2006-01-02") {
			continue
		}
		data, err := s.get(ctx, "https://www.nseindia.com/api/ipo-detail?symbol="+url.QueryEscape(item.Symbol)+"&series="+map[string]string{"SME": "SME", "Mainboard": "EQ"}[item.Board])
		if err == nil {
			err = parseIPODetail(data, item)
		}
		if err != nil {
			results["detail:"+item.Symbol] = "failed"
		} else {
			results["detail:"+item.Symbol] = "ok"
		}
		if err = s.enrich(ctx, item); err != nil {
			results["enrichment:"+item.Symbol] = "unavailable"
		} else {
			results["enrichment:"+item.Symbol] = "ok"
		}
		if ctx.Err() != nil {
			results["deadline"] = "failed"
			break
		}
	}
	return items, results
}

func parseIPODetail(data []byte, item *ipoSourceIssue) error {
	var body struct {
		Info struct {
			Symbol string `json:"symbol"`
			Rows   []struct {
				Title *string         `json:"title"`
				Value json.RawMessage `json:"value"`
			} `json:"dataList"`
		} `json:"issueInfo"`
		Bids []map[string]json.RawMessage `json:"bidDetails"`
	}
	if json.Unmarshal(data, &body) != nil || body.Info.Symbol != item.Symbol || len(body.Info.Rows) == 0 {
		return errors.New("unverified IPO detail")
	}
	for _, row := range body.Info.Rows {
		if row.Title == nil {
			continue
		}
		value := sourceText(row.Value)
		switch strings.ToLower(strings.TrimSpace(*row.Title)) {
		case "bid lot", "lot size":
			if nums := sourceNumericPattern.FindString(value); nums != "" {
				item.Lot = positiveLot(strings.ReplaceAll(nums, ",", ""))
			}
		case "minimum order quantity":
			if item.Lot == 0 {
				item.Lot = positiveLot(strings.ReplaceAll(sourceNumericPattern.FindString(value), ",", ""))
			}
		case "name of the registrar":
			if optionalText(&value, 512) {
				item.Registrar = value
			}
		case "issue size":
			item.IssueText = value
		}
	}
	sharePattern := regexp.MustCompile(`(?i)([0-9][0-9,]*)\s+equity\s+shares`)
	if matches := sharePattern.FindAllStringSubmatch(item.IssueText, -1); len(matches) == 1 {
		item.IssueShares = sourceNumber(matches[0][1], true)
	}
	keys := map[string]string{"1": "qib", "2": "nii", "2.1": "bhni", "2.2": "shni", "3": "retail", "4": "employee"}
	for _, row := range body.Bids {
		label, serial := sourceText(row["category"]), sourceText(row["srNo"])
		key, ok := keys[serial]
		if strings.EqualFold(label, "Total") {
			key, ok = "total", true
		}
		if !ok || label == "" {
			continue
		}
		if item.Board == "SME" && key == "retail" {
			key = "individual"
		}
		bid := sourceNumber(sourceText(row["noOfsharesBid"]), true)
		if bid == nil {
			bid = sourceNumber(sourceText(row["noOfshareBid"]), true)
		}
		offered := sourceNumber(sourceText(row["noOfSharesOffered"]), true)
		multiple := sourceNumber(sourceText(row["noOfTime"]), false)
		if offered == nil || *offered == "0" {
			multiple = nil
		}
		entry := ipoSubscription{key, label, offered, bid, sourceNumber(sourceText(row["noofapplication"]), true), multiple}
		if offered == nil && bid == nil && entry.Applications == nil {
			continue
		}
		replaced := false
		for j := range item.Subscriptions {
			if item.Subscriptions[j].Category == key {
				item.Subscriptions[j] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			item.Subscriptions = append(item.Subscriptions, entry)
		}
	}
	item.DetailOK = true
	return nil
}
