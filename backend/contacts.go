package main

import (
	"net/http"
	"strings"
)

func (a *api) importContacts(w http.ResponseWriter, r *http.Request, user actor) {
	var input struct {
		Contacts []struct {
			Phone string `json:"phone_number"`
			Name  string `json:"name"`
		} `json:"contacts"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Contacts) < 1 || len(input.Contacts) > 100 {
		fail(w, 400, "invalid_contacts", "Supply 1–100 contacts per request")
		return
	}
	seen := make(map[string]bool, len(input.Contacts))
	for i := range input.Contacts {
		c := &input.Contacts[i]
		c.Name = strings.TrimSpace(c.Name)
		if !phonePattern.MatchString(c.Phone) || !validText(c.Name, 256) || seen[c.Phone] {
			fail(w, 400, "invalid_contacts", "Supply distinct international phone numbers and names of 1–256 bytes")
			return
		}
		seen[c.Phone] = true
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	for _, c := range input.Contacts {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ipo_pos.contact_names AS n
            (business_id,phone_number,supplied_by_user_id,name) VALUES($1,$2,$3,$4)
            ON CONFLICT (business_id,phone_number,supplied_by_user_id) DO UPDATE
            SET name=EXCLUDED.name,updated_at=now() WHERE n.name IS DISTINCT FROM EXCLUDED.name`,
			user.BusinessID, c.Phone, user.UserID, c.Name)
		if err != nil {
			dbFailure(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"accepted": len(input.Contacts)})
}

func (a *api) lookupContact(w http.ResponseWriter, r *http.Request, user actor) {
	phone := r.URL.Query().Get("phone_number")
	if !phonePattern.MatchString(phone) {
		fail(w, 400, "invalid_phone", "Supply phone_number in international format (URL-encode + as %2B)")
		return
	}
	// Names are suggestions, not proof of identity; never merge customers by name.
	queryJSON(w, r, a.db, 200, `WITH customers AS (
        SELECT id,name,name_supplied_by_user_id FROM ipo_pos.customers
        WHERE business_id=$1 AND phone_number=$2 AND archived_at IS NULL
    ), names AS (
        SELECT n.*,u.name AS supplied_by_name FROM ipo_pos.contact_names n
        JOIN ipo_pos.users u ON u.id=n.supplied_by_user_id
        WHERE n.business_id=$1 AND n.phone_number=$2
    ) SELECT jsonb_build_object('phone_number',$2::text,
        'customer_count',(SELECT count(*) FROM customers),
        'customers',COALESCE((SELECT jsonb_agg(to_jsonb(c)) FROM
            (SELECT * FROM customers ORDER BY id LIMIT 100) c),'[]'::jsonb),
        'source_count',(SELECT count(*) FROM names),
        'name_sources',COALESCE((SELECT jsonb_agg(to_jsonb(n)-'business_id'-'phone_number') FROM
            (SELECT * FROM names ORDER BY updated_at DESC,supplied_by_user_id LIMIT 100) n),'[]'::jsonb),
        'suggested_name',CASE WHEN (SELECT count(*) FROM customers)=1 THEN
            (SELECT jsonb_build_object('name',name,'source','customer_record','customer_id',id,
                'supplied_by_user_id',name_supplied_by_user_id) FROM customers)
            ELSE (SELECT jsonb_build_object('name',name,'source',source,'supplied_by_user_id',supplied_by_user_id)
                FROM names ORDER BY updated_at DESC,supplied_by_user_id LIMIT 1) END)`, user.BusinessID, phone)
}

func (a *api) removeContactName(w http.ResponseWriter, r *http.Request, user actor) {
	phone := r.URL.Query().Get("phone_number")
	if !phonePattern.MatchString(phone) {
		fail(w, 400, "invalid_phone", "Supply an international phone_number")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ipo_pos.contact_names
        WHERE business_id=$1 AND phone_number=$2 AND supplied_by_user_id=$3`, user.BusinessID, phone, user.UserID); err != nil {
		dbFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
