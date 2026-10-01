package postgres

import (
	"database/sql"
	"testing"
)

const migration0223Base = 222

// TestMigration0223KeepsExistingWebhooksOnTheRawEvent checks that every webhook channel created
// before envelopes keeps receiving the raw event at the detail class, that other channels are
// untouched, and that the raw body cannot be stored below detail or on another channel type.
func TestMigration0223KeepsExistingWebhooksOnTheRawEvent(t *testing.T) {
	inTenant := func(db *sql.DB, stmt string) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('app.current_tenant','t-raw',true)`); err != nil {
			return err
		}
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
		return tx.Commit()
	}
	_, db := ownershipTestDatabase(t, migration0223Base, func(db *sql.DB) {
		if _, err := db.Exec(`INSERT INTO tenants(id,name) VALUES('t-raw','Raw') ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{
			`INSERT INTO notification_channels(tenant_id,id,name,channel_type,created_at,updated_at) VALUES('t-raw','hook','Hook','webhook',now(),now())`,
			`INSERT INTO notification_channels(tenant_id,id,name,channel_type,created_at,updated_at,data_class) VALUES('t-raw','room','Room','slack',now(),now(),'signal')`,
		} {
			if err := inTenant(db, stmt); err != nil {
				t.Fatal(err)
			}
		}
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT set_config('app.current_tenant','t-raw',true)`); err != nil {
		t.Fatal(err)
	}
	type state struct {
		raw   bool
		class string
	}
	got := map[string]state{}
	rows, err := tx.Query(`SELECT id,raw_event,data_class FROM notification_channels WHERE tenant_id='t-raw'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var s state
		if err := rows.Scan(&id, &s.raw, &s.class); err != nil {
			t.Fatal(err)
		}
		got[id] = s
	}
	_ = rows.Close()
	_ = tx.Rollback()
	if got["hook"] != (state{true, "detail"}) || got["room"] != (state{false, "signal"}) {
		t.Fatalf("after 0223 = %+v, want the webhook on the raw event at detail and the slack channel unchanged", got)
	}
	for name, stmt := range map[string]string{
		"raw below detail": `UPDATE notification_channels SET data_class='summary' WHERE id='hook'`,
		"raw on slack":     `UPDATE notification_channels SET raw_event=true WHERE id='room'`,
	} {
		if err := inTenant(db, stmt); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
	if err := inTenant(db, `INSERT INTO notification_channels(tenant_id,id,name,channel_type,created_at,updated_at) VALUES('t-raw','new','New','webhook',now(),now())`); err != nil {
		t.Fatal(err)
	}
}
