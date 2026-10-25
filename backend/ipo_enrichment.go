package main

import (
	"context"
	"errors"
	"io"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

const ipoEnrichmentIndex = "https://ipowatch.in/upcoming-ipo-list/"

var errIPOEnrichmentPageMissing = errors.New("enrichment page missing")

// Verified source titles are aliases only for this NSE issuer and source URL.
var ipoEnrichmentMappings = map[string]struct{ name, title, url string }{
	"TONBO":   {"Tonbo Imaging India Limited", "Tonbo Imaging", "https://ipowatch.in/tonbo-imaging-india-ipo/"},
	"MKSONS":  {"M. K. Sons Fine Jewels Limited", "M.K.Sons Fine Jewels", "https://ipowatch.in/mksons-fine-jewels-ipo/"},
	"ZETWERK": {"Zetwerk Manufacturing Businesses Limited", "Zetwerk Manufacturing", "https://ipowatch.in/zetwerk-ipo/"},
}

func htmlText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return ""
	}
	var text strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(htmlText(child))
		text.WriteByte(' ')
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func htmlTables(n *html.Node) [][][]string {
	var tables [][][]string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "table" {
			var rows [][]string
			var walk func(*html.Node)
			walk = func(r *html.Node) {
				if r.Type == html.ElementNode && r.Data == "tr" {
					cells := []string{}
					for c := r.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
							cells = append(cells, htmlText(c))
						}
					}
					if len(cells) > 0 {
						rows = append(rows, cells)
					}
					return
				}
				for c := r.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(node)
			if len(rows) > 0 {
				tables = append(tables, rows)
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return tables
}

func issuerKey(text string) string {
	text = regexp.MustCompile(`(?i)\b(limited|ltd|private|pvt)\b`).ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
}

func enrichmentLinkKey(name string) string {
	name = regexp.MustCompile(`(?i)\bipo\b.*$`).ReplaceAllString(name, "")
	name = regexp.MustCompile(`(?i)\b(india|indian|the|company|co)\b`).ReplaceAllString(name, "")
	return issuerKey(name)
}

func parseIPOEnrichmentLinks(reader io.Reader) (map[string]string, error) {
	doc, err := html.Parse(reader)
	if err != nil {
		return nil, errors.New("invalid enrichment index HTML")
	}
	base, _ := url.Parse(ipoEnrichmentIndex)
	pagePath := regexp.MustCompile(`^/[a-z0-9-]+-ipo/?$`)
	links := map[string]string{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key != "href" {
					continue
				}
				target, err := base.Parse(attr.Val)
				if err != nil || target.Scheme != "https" || target.Host != "ipowatch.in" || target.User != nil || target.RawQuery != "" || target.RawPath != "" || !pagePath.MatchString(target.Path) {
					continue
				}
				target.Fragment = ""
				target.Path = strings.TrimRight(target.Path, "/") + "/"
				key := enrichmentLinkKey(htmlText(n))
				if len(key) < 5 {
					continue
				}
				if previous, exists := links[key]; exists && previous != target.String() {
					links[key] = ""
				} else if !exists {
					links[key] = target.String()
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return links, nil
}

func percentage(text string) *string {
	if !strings.Contains(text, "%") || strings.ContainsAny(text, "-()") {
		return nil
	}
	value := sourceNumber(sourceNumericPattern.FindString(text), false)
	if value == nil {
		return nil
	}
	n, _ := new(big.Rat).SetString(*value)
	if n.Cmp(big.NewRat(100, 1)) > 0 {
		return nil
	}
	return value
}

func parseIPOEnrichment(reader io.Reader, item *ipoSourceIssue, source string) error {
	doc, err := html.Parse(reader)
	if err != nil {
		return errors.New("invalid enrichment HTML")
	}
	key := issuerKey(item.Name)
	keys := []string{key}
	if mapping, ok := ipoEnrichmentMappings[item.Symbol]; ok && issuerKey(mapping.name) == key && mapping.url == source {
		keys = append(keys, issuerKey(mapping.title))
	}
	verified, missing := false, false
	var identify func(*html.Node)
	identify = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "h1" || n.Data == "title") {
			heading := issuerKey(htmlText(n))
			if strings.HasPrefix(heading, "pagenotfound") || strings.HasPrefix(heading, "404pagenotfound") {
				missing = true
			}
			for _, candidate := range keys {
				if strings.HasPrefix(heading, candidate+"ipo") {
					verified = true
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			identify(child)
		}
	}
	identify(doc)
	if missing {
		return errIPOEnrichmentPageMissing
	}
	if len(key) < 5 || !verified {
		return errors.New("enrichment issuer unverified")
	}
	tables := htmlTables(doc)
	found := false
	// Later matching tables replace template duplicates, as in the upstream extractor.
	for _, rows := range tables {
		if len(rows) == 0 {
			continue
		}
		header := strings.ToLower(strings.Join(rows[0], " "))
		if strings.Contains(header, "investor category") {
			reservations := []ipoReservation{}
			for _, row := range rows[1:] {
				if len(row) < 2 {
					continue
				}
				p := percentage(row[len(row)-1])
				var shares *string
				if len(row) > 2 {
					shares = sourceNumber(row[1], true)
				}
				if shares != nil || p != nil {
					reservations = append(reservations, ipoReservation{row[0], "As labelled by IPO Watch; denominator not independently confirmed", shares, p})
				}
			}
			if len(reservations) > 0 {
				item.Reservations = reservations
				found = true
			}
		}
		if strings.Contains(header, "particular") && strings.Contains(header, "pre ipo") {
			pre, post := -1, -1
			for column, label := range rows[0] {
				label = strings.ToLower(label)
				if strings.Contains(label, "pre") && strings.Contains(label, "%") {
					pre = column
				}
				if strings.Contains(label, "post") && strings.Contains(label, "%") {
					post = column
				}
			}
			// Some tables use separate 'No. of Shares' and '%' subheadings.
			start := 1
			if pre < 0 || post < 0 {
				if len(rows[0]) == 3 {
					pre, post = 1, 2
				} else if len(rows) > 1 && len(rows[1]) >= 5 {
					pre, post = 2, 4
					if strings.Contains(strings.Join(rows[1], " "), "%") && strings.Contains(strings.ToLower(strings.Join(rows[1], " ")), "shares") {
						start = 2
					}
				}
			}
			holdings := []ipoShareholding{}
			for _, row := range rows[start:] {
				if pre < 0 || post < 0 || len(row) <= max(pre, post) || strings.EqualFold(row[0], "total") {
					continue
				}
				readPct := func(text string) *string {
					if !strings.Contains(text, "%") {
						text += "%"
					}
					return percentage(text)
				}
				a, b := readPct(row[pre]), readPct(row[post])
				if a != nil || b != nil {
					holdings = append(holdings, ipoShareholding{row[0], a, b})
				}
			}
			if len(holdings) > 0 {
				item.Shareholdings = holdings
				found = true
			}
		}
		for _, row := range rows {
			if len(row) < 2 {
				continue
			}
			label, value := strings.ToLower(strings.TrimSuffix(row[0], ":")), strings.TrimSpace(row[1])
			if strings.Contains(label, "lock-in period end date") {
				if date := sourceDate(value); date != nil {
					item.Lockins = append(item.Lockins, ipoLockin{row[0], *date})
					found = true
				}
			}
			for _, name := range []string{"ipo open", "ipo close", "ipo listing", "basis of allotment", "refund", "credit to demat"} {
				if strings.HasPrefix(label, name) && value != "" && value != "-" {
					precision := "text"
					if date := sourceDate(value); date != nil {
						value, precision = *date, "date"
					}
					if item.Timeline == nil {
						item.Timeline = map[string]any{}
					}
					item.Timeline[name] = map[string]string{"value": value, "precision": precision, "status": "expected", "source_url": source}
					found = true
				}
			}
			if label == "ipo issue size" || label == "issue size" || label == "total issue size" {
				if strings.Contains(strings.ToLower(value), "crore") {
					if number := sourceNumber(sourceNumericPattern.FindString(value), false); number != nil {
						n, _ := new(big.Rat).SetString(*number)
						amount := new(big.Rat).Mul(n, big.NewRat(10000000, 1)).FloatString(2)
						item.IssueAmount = &amount
						found = true
					}
				}
			}
		}
	}
	if !found {
		return errors.New("enrichment fields unavailable")
	}
	item.EnrichmentURL, item.EnrichmentOK = source, true
	return nil
}

func (s *ipoSourceClient) enrich(ctx context.Context, item *ipoSourceIssue, links map[string]string) error {
	var targets []string
	if mapping, ok := ipoEnrichmentMappings[item.Symbol]; ok && issuerKey(mapping.name) == issuerKey(item.Name) {
		targets = append(targets, mapping.url)
	}
	if target := links[enrichmentLinkKey(item.Name)]; target != "" {
		targets = append(targets, target)
	}
	name := regexp.MustCompile(`(?i)\b(limited|ltd|private|pvt)\b`).ReplaceAllString(item.Name, "")
	for _, candidate := range []string{name, regexp.MustCompile(`(?i)\b(india|indian)\b`).ReplaceAllString(name, "")} {
		slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(candidate), "-"), "-")
		targets = append(targets, "https://ipowatch.in/"+slug+"-ipo/")
	}
	seen := map[string]bool{}
	resultErr := errIPOEnrichmentPageMissing
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		data, err := s.get(ctx, target)
		if err == nil {
			if err = parseIPOEnrichment(strings.NewReader(string(data)), item, target); err == nil {
				return nil
			}
			if err != errIPOEnrichmentPageMissing {
				resultErr = err
			}
		} else if resultErr == errIPOEnrichmentPageMissing {
			resultErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return resultErr
}
