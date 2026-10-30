package storage

import "testing"

func TestCatalogPricingUpgradeRetainsFrozenPrices(t *testing.T) {
	db := migrationDB(t)
	migrateTaskDefaultPaths(t, db, UsageDatabase, taskDefaultBefore(taskDefaultMigrationPaths(t, UsageDatabase), "migrations/usage/0005_catalog_pricing.sql"))
	if _, e := db.Exec(`INSERT INTO requests(id,scope,turn_id,at,model,input_estimate,output_cap,original) VALUES('old','owner','turn',1,'model',1,1,'{}'); INSERT INTO model_prices VALUES('provider','','model',0,'{}'); INSERT INTO request_accounting(request_id,stage,price,cost_usd,cost_status) VALUES('old','assistant_processing','{"version":"old","input":"2","output":"8"}','0.001','priced')`); e != nil {
		t.Fatal(e)
	}
	if e := Migrate(t.Context(), db, UsageDatabase); e != nil {
		t.Fatal(e)
	}
	var price, cost string
	if e := db.QueryRow(`SELECT price,cost_usd FROM request_accounting WHERE request_id='old'`).Scan(&price, &cost); e != nil || cost != "0.001" || price != `{"version":"old","input":"2","output":"8"}` {
		t.Fatal(price, cost, e)
	}
	if _, e := db.Exec(`SELECT * FROM model_prices`); e == nil {
		t.Fatal("manual prices survived migration")
	}
}
