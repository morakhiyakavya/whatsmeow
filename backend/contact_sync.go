package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func validDirectoryRevision(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}

// Each page and its revision share one PostgreSQL snapshot. Changed revisions require a restart.
func (a *api) syncContactDirectory(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind, id := q.Get("after_kind"), q.Get("after_id")
	expected, since := q.Get("revision"), q.Get("since_revision")
	validCursor := kind == "" && id == "" || kind == "customer" && validUUID(id) ||
		(kind == "contact" || kind == "registration") && phonePattern.MatchString(id)
	if p.After != nil || !validCursor || expected != "" && !validDirectoryRevision(expected) ||
		since != "" && !validDirectoryRevision(since) || kind != "" && (expected == "" || since != "") {
		fail(w, 400, "invalid_sync_cursor", "Supply a directory revision and the returned kind/ID cursor")
		return
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `WITH version AS MATERIALIZED (
		SELECT COALESCE((SELECT revision FROM ipo_pos.contact_directory_revisions WHERE business_id=$1),0)::text AS revision
	), display_names AS (`+contactDisplayNames+`), entries AS (
		(SELECT 'customer'::text AS kind,c.id::text AS id,to_jsonb(c) AS data FROM ipo_pos.customers c
		WHERE c.business_id=$1 AND c.archived_at IS NULL AND ($4<'customer' OR
			$4='customer' AND c.id>NULLIF(CASE WHEN $4='customer' THEN $5 END,'')::uuid)
		ORDER BY c.id LIMIT ($6+1))
		UNION ALL (SELECT 'contact',d.phone_number,to_jsonb(d) FROM display_names d
			WHERE $4<'contact' OR $4='contact' AND d.phone_number>$5 ORDER BY d.phone_number LIMIT ($6+1))
		UNION ALL (SELECT 'registration',n.phone_number,jsonb_build_object('phone_number',n.phone_number,
			'registered',n.registered,'checked_at',n.checked_at) FROM ipo_pos.whatsapp_number_checks n
			WHERE n.business_id=$1 AND ($4<'registration' OR $4='registration' AND n.phone_number>$5)
			ORDER BY n.phone_number LIMIT ($6+1))
	), matches AS (
		SELECT e.* FROM entries e CROSS JOIN version v WHERE (kind,id)>($4,$5)
		AND ($7='' OR v.revision<>$7) AND ($8='' OR v.revision=$8)
		ORDER BY kind,id LIMIT ($6+1)
	), visible AS (SELECT * FROM matches ORDER BY kind,id LIMIT $6)
	SELECT jsonb_build_object('revision',v.revision,'unchanged',v.revision=$7,
		'items',COALESCE((SELECT jsonb_agg(to_jsonb(e) ORDER BY kind,id) FROM visible e),'[]'::jsonb),
		'next_kind',CASE WHEN (SELECT count(*) FROM matches)>$6 THEN (SELECT kind FROM visible ORDER BY kind DESC,id DESC LIMIT 1) END,
		'next_id',CASE WHEN (SELECT count(*) FROM matches)>$6 THEN (SELECT id FROM visible ORDER BY kind DESC,id DESC LIMIT 1) END)
	FROM version v`, user.BusinessID, nil, user.UserID, kind, id, p.Limit, since, expected).Scan(&data)
	if err != nil {
		dbFailure(w, err)
		return
	}
	var result struct {
		Revision string `json:"revision"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		dbFailure(w, err)
		return
	}
	if expected != "" && expected != result.Revision {
		fail(w, 409, "directory_changed", "The directory changed; restart synchronisation")
		return
	}
	writeJSON(w, 200, data)
}
