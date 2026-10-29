package main

import (
	"encoding/json"
	"net/http"
)

// Revision and records are read in one snapshot; a changed revision invalidates pagination.
func (a *api) syncBusinessData(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	since, expected := q.Get("since_revision"), q.Get("revision")
	after, kind, id := q.Get("after_revision"), q.Get("after_kind"), q.Get("after_id")
	if since == "" {
		since = "0"
	}
	cursor := after != "" || kind != "" || id != ""
	if p.After != nil || !validDirectoryRevision(since) || expected != "" && !validDirectoryRevision(expected) ||
		cursor && (!validDirectoryRevision(after) || !validUUID(id) || expected == "" ||
			kind != "transaction" && kind != "assumptions" && kind != "ipo") {
		fail(w, 400, "invalid_sync_cursor", "Supply a revision and the returned change cursor")
		return
	}
	if !cursor {
		after, kind, id = "0", "", "00000000-0000-0000-0000-000000000000"
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `WITH version AS MATERIALIZED (
  SELECT COALESCE((SELECT revision FROM ipo_pos.business_data_revisions WHERE business_id=$1),0) AS revision
 ), changes AS MATERIALIZED (
  SELECT c.* FROM ipo_pos.business_data_changes c CROSS JOIN version v WHERE c.business_id=$1
   AND c.revision>$2::bigint AND (c.revision,c.kind,c.record_id)>($3::bigint,$4::text,$5::uuid)
   AND ($7='' OR v.revision::text=$7) AND v.revision>=$2::bigint
   ORDER BY c.revision,c.kind,c.record_id LIMIT ($6+1)
 ), visible AS (SELECT * FROM changes ORDER BY revision,kind,record_id LIMIT $6), bodies AS (
  SELECT c.*,CASE c.kind
   WHEN 'transaction' THEN (SELECT `+transactionJSON+` FROM ipo_pos.transactions t WHERE t.business_id=$1 AND t.id=c.record_id)
   WHEN 'assumptions' THEN (SELECT `+assumptionJSON+` FROM ipo_pos.ipo_dashboard_assumption_versions v
    WHERE v.business_id=$1 AND v.ipo_id=c.record_id ORDER BY v.version DESC LIMIT 1)
	   WHEN 'ipo' THEN (SELECT `+ipoCatalogueJSON+` || jsonb_build_object('selectable_until',COALESCE(lifecycle.listing_date,i.closes_on+7),'source_basis',
    (SELECT to_jsonb(b) FROM ipo_pos.ipo_dashboard_basis_versions b WHERE b.ipo_id=i.id ORDER BY b.version DESC LIMIT 1))
    FROM ipo_pos.ipos i `+ipoLifecycleJoin+` WHERE i.id=c.record_id) END AS data FROM visible c
 ) SELECT jsonb_build_object('revision',v.revision::text,'unchanged',v.revision=$2::bigint,
  'items',COALESCE((SELECT jsonb_agg(jsonb_build_object('kind',kind,'id',record_id,'data',data)
   ORDER BY revision,kind,record_id) FROM bodies),'[]'::jsonb),
  'next_revision',CASE WHEN (SELECT count(*) FROM changes)>$6 THEN
   (SELECT revision::text FROM visible ORDER BY revision DESC,kind DESC,record_id DESC LIMIT 1) END,
  'next_kind',CASE WHEN (SELECT count(*) FROM changes)>$6 THEN
   (SELECT kind FROM visible ORDER BY revision DESC,kind DESC,record_id DESC LIMIT 1) END,
  'next_id',CASE WHEN (SELECT count(*) FROM changes)>$6 THEN
   (SELECT record_id FROM visible ORDER BY revision DESC,kind DESC,record_id DESC LIMIT 1) END)
  FROM version v`, user.BusinessID, since, after, kind, id, p.Limit, expected).Scan(&data)
	if err != nil {
		dbFailure(w, err)
		return
	}
	var result struct {
		Revision  string `json:"revision"`
		Unchanged bool   `json:"unchanged"`
		Items     []struct {
			Kind string          `json:"kind"`
			ID   string          `json:"id"`
			Data json.RawMessage `json:"data"`
		} `json:"items"`
		NextRevision *string `json:"next_revision"`
		NextKind     *string `json:"next_kind"`
		NextID       *string `json:"next_id"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		dbFailure(w, err)
		return
	}
	if expected != "" && result.Revision != expected || revisionLess(result.Revision, since) {
		fail(w, 409, "business_data_changed", "Business data changed; restart synchronisation")
		return
	}
	for i := range result.Items {
		entry := &result.Items[i]
		if entry.Kind == "ipo" && string(entry.Data) != "null" {
			entry.Data, err = decorateIPO(entry.Data)
			if err != nil {
				dbFailure(w, err)
				return
			}
		}
	}
	writeJSON(w, 200, result)
}

func revisionLess(a, b string) bool { return len(a) < len(b) || len(a) == len(b) && a < b }
