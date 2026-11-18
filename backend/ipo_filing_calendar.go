package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const sebiIPOHost = "www.sebi.gov.in"
const sebiIPOSearch = "https://" + sebiIPOHost + "/sebiweb/home/HomeAction.do?doListingAll=yes&sid=3&search="

type ipoCalendarEntry struct {
	Name, Type, IssueStatus, OpenDate, CloseDate string
}

func parseIPOFilingSchedule(data []byte) ([]ipoCalendarEntry, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	const flight = "self.__next_f.push("
	const key = `"initialIpos":`
	// shortcut: IPO Watch embeds calendar rows in its page payload, switch to a documented calendar feed when available.
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != "script" || node.FirstChild == nil {
			continue
		}
		payload, ok := strings.CutPrefix(node.FirstChild.Data, flight)
		if !ok {
			continue
		}
		var parts []json.RawMessage
		if json.NewDecoder(strings.NewReader(payload)).Decode(&parts) != nil || len(parts) != 2 {
			continue
		}
		var content string
		if json.Unmarshal(parts[1], &content) != nil {
			continue
		}
		_, entries, found := strings.Cut(content, key)
		if found {
			var rows []ipoCalendarEntry
			err := json.NewDecoder(strings.NewReader(entries)).Decode(&rows)
			return rows, err
		}
	}
	return nil, errors.New("calendar schedule unavailable")
}

var filingPagePath = regexp.MustCompile(`^/filings/public-issues/[a-z]{3}-[0-9]{4}/[a-z0-9-]+_[0-9]+\.html$`)
var filingTitleSuffix = regexp.MustCompile(`(?i)\s*[-–]\s*(?:updated\s+)?(?:u?drhp|rhp)(?:\s*-?\s*[ivx]+)?\s*$`)
var filingIdentityPattern = regexp.MustCompile(`^[a-z0-9]+$`)

func matchIPOFiling(data []byte, name string) (string, string) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return "", ""
	}
	issuer := enrichmentLinkKey(name)
	var matchName, matchURL string
	for row := range doc.Descendants() {
		if row.Type != html.ElementNode || row.Data != "tr" {
			continue
		}
		rowText := htmlText(row)
		if !strings.Contains(rowText, "Draft Offer Documents filed with SEBI") &&
			!strings.Contains(rowText, "Red Herring Documents filed with ROC") {
			continue
		}
		for node := range row.Descendants() {
			if node.Type != html.ElementNode || node.Data != "a" {
				continue
			}
			legalName := filingTitleSuffix.ReplaceAllString(htmlText(node), "")
			if enrichmentLinkKey(legalName) != issuer || !validText(legalName, 256) {
				continue
			}
			for _, attr := range node.Attr {
				if attr.Key != "href" {
					continue
				}
				u, err := url.Parse(attr.Val)
				if err != nil || u.Scheme != "https" || u.Host != sebiIPOHost || u.User != nil ||
					u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || !filingPagePath.MatchString(u.Path) {
					continue
				}
				if matchName != "" && issuerKey(matchName) != issuerKey(legalName) {
					return "", ""
				}
				if matchName == "" {
					matchName, matchURL = legalName, attr.Val
				}
			}
		}
	}
	return matchName, matchURL
}

func (s *ipoSourceClient) collectFilingCalendar(ctx context.Context, data []byte, items []ipoSourceIssue, today string, results map[string]string) []ipoSourceIssue {
	rows, err := parseIPOFilingSchedule(data)
	if err != nil {
		results["filing calendar"] = "calendar schedule unavailable"
		return items
	}
	results["filing calendar"] = "ok"
	known := make(map[string]bool, len(items))
	for _, item := range items {
		known[enrichmentLinkKey(item.Name)] = true
	}
	for _, row := range rows {
		open, close := sourceDate(row.OpenDate), sourceDate(row.CloseDate)
		nameKey := enrichmentLinkKey(row.Name)
		if row.Type != "Mainboard" || row.IssueStatus != "Upcoming" || known[nameKey] ||
			len(nameKey) < 5 || !validText(row.Name, 256) || open == nil || close == nil || *open > *close || *close < today {
			continue
		}
		page, err := s.get(ctx, sebiIPOSearch+url.QueryEscape(row.Name))
		if err != nil {
			results["filing calendar"] = err.Error()
			continue
		}
		name, filing := matchIPOFiling(page, row.Name)
		if filing == "" || !filingIdentityPattern.MatchString(issuerKey(name)) {
			continue
		}
		known[nameKey] = true
		items = append(items, ipoSourceIssue{Name: name, Board: row.Type, FilingURL: filing, Timeline: map[string]any{
			"opening": map[string]string{"value": *open, "precision": "date", "status": "expected", "source_url": ipoEnrichmentIndex},
			"closing": map[string]string{"value": *close, "precision": "date", "status": "expected", "source_url": ipoEnrichmentIndex},
		}})
	}
	return items
}

// Keep this ASCII key aligned with issuerKey for source adoption.
const storedIPOIssuerKey = `regexp_replace(regexp_replace(lower(i.name), '\m(limited|ltd|private|pvt)\M', '', 'g'), '[^a-z0-9]', '', 'g')`

func saveFilingIPOs(ctx context.Context, db *sql.DB, items []ipoSourceIssue) (int, error) {
	type filingIPO struct {
		Key, Name, Board, URL string
		Timeline              map[string]any
	}
	rows := make([]filingIPO, 0, len(items))
	for _, item := range items {
		rows = append(rows, filingIPO{issuerKey(item.Name), item.Name, item.Board, item.FilingURL, item.Timeline})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return 0, err
	}
	result, err := db.ExecContext(ctx, `INSERT INTO ipo_pos.ipos AS saved
	 (code,name,issuer_name,catalogue_source,source_issue_id,board,source_url,last_seen_at,timeline)
	 SELECT 'FILING:'||x."Key",x."Name",x."Name",'filing',x."Key",x."Board",x."URL",clock_timestamp(),x."Timeline"
	 FROM jsonb_to_recordset($1::jsonb) AS x("Key" text,"Name" text,"Board" text,"URL" text,"Timeline" jsonb)
	 WHERE NOT EXISTS (SELECT 1 FROM ipo_pos.ipos i WHERE i.catalogue_source='nse' AND i.board=x."Board"
	   AND `+storedIPOIssuerKey+`=x."Key"
	   AND (i.closes_on IS NULL OR i.closes_on >= (now() AT TIME ZONE 'Asia/Kolkata')::date-7))
	 ON CONFLICT(catalogue_source,source_issue_id) DO UPDATE SET source_url=excluded.source_url,
	   last_seen_at=excluded.last_seen_at, timeline=excluded.timeline
	 WHERE saved.board=excluded.board AND saved.archived_at IS NULL`, string(encoded))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func expectedIPODate(data json.RawMessage, key string) string {
	var timeline map[string]struct{ Value, Status, Precision string }
	if json.Unmarshal(data, &timeline) != nil {
		return ""
	}
	date := timeline[key]
	if date.Status == "expected" && date.Precision == "date" {
		if parsed, err := time.Parse(time.DateOnly, date.Value); err == nil {
			return parsed.Format(time.DateOnly)
		}
	}
	return ""
}
