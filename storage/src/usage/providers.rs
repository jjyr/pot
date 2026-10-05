//! Provider allowances supplement, never replace, the global allowance.
use super::*;
use rusqlite::OptionalExtension;

impl Ledger {
    pub fn provider_policy(&self, provider: &str) -> Result<(u64, Policy)> {
        let value: Option<String> = self
            .0
            .query_row(
                "SELECT value FROM settings WHERE key=?",
                [format!("provider:{provider}")],
                |r| r.get(0),
            )
            .optional()?;
        Ok(value
            .map(|s| serde_json::from_str(&s))
            .transpose()?
            .unwrap_or_default())
    }
    pub fn provider_quota(&self, provider: &str, at: i64) -> Result<Value> {
        let (revision, p) = self.provider_policy(provider)?;
        if p.period == "none" {
            return Ok(json!({"provider":provider,"revision":revision,"policy":p,"active":false}));
        }
        let (start, end) = p.window(at)?;
        let (used,reserved):(u64,u64) = self.0.query_row("SELECT COALESCE(SUM(COALESCE(r.input,0)+COALESCE(r.output,0)),0),COALESCE(SUM(r.reserved),0) FROM requests r JOIN request_routes t ON t.request_id=r.id WHERE t.provider=? AND r.at>=? AND r.at<?",params![provider,start,end],|r|Ok((r.get(0)?,r.get(1)?)))?;
        Ok(
            json!({"provider":provider,"revision":revision,"policy":p,"active":true,"used":used,"reserved":reserved,"remaining":p.limit.saturating_sub(used.saturating_add(reserved)),"reset_at":end}),
        )
    }
    pub fn set_provider_policy(
        &self,
        provider: &str,
        revision: u64,
        policy: &Policy,
    ) -> Result<()> {
        policy.validate()?;
        anyhow::ensure!(
            self.provider_policy(provider)?.0 == revision,
            "Provider allowance changed; refresh before saving"
        );
        let next = revision
            .checked_add(1)
            .ok_or_else(|| anyhow!("Revision overflow"))?;
        self.0.execute(
            "INSERT INTO settings VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            params![
                format!("provider:{provider}"),
                serde_json::to_string(&(next, policy))?
            ],
        )?;
        Ok(())
    }
    pub fn reserve_routed(
        &self,
        id: &str,
        scope: &str,
        turn: &str,
        at: i64,
        spec: &agent::ResolvedSpec,
        epoch: u64,
        input: u64,
        output: u64,
    ) -> Result<Option<u64>> {
        let q = self.provider_quota(&spec.provider, at)?;
        let output = if q["active"] == true {
            let available = q["remaining"].as_u64().unwrap_or(0).saturating_sub(input);
            if available == 0 {
                return Ok(None);
            }
            output.min(available)
        } else {
            output
        };
        // Reservation and route must commit together: another lane cannot miss this provider claim.
        self.0.execute_batch("SAVEPOINT routed_reservation")?;
        let result = (|| -> Result<Option<u64>> {
            let value = self.reserve(id, scope, turn, at, &spec.model, input, output)?;
            if value.is_some() {
                self.route(id, spec, epoch)?;
            }
            Ok(value)
        })();
        match result {
            Ok(value) => {
                self.0.execute_batch("RELEASE routed_reservation")?;
                Ok(value)
            }
            Err(e) => {
                let _ = self
                    .0
                    .execute_batch("ROLLBACK TO routed_reservation; RELEASE routed_reservation");
                Err(e)
            }
        }
    }
    pub fn wait_routed(
        &self,
        scope: &str,
        turn: &str,
        at: i64,
        required: u64,
        provider: &str,
    ) -> Result<()> {
        let global = self.quota(at)?;
        let local = self.provider_quota(provider, at)?;
        let reset = [&global, &local]
            .iter()
            .filter(|q| q["active"] == true && q["remaining"].as_u64().unwrap_or(0) < required)
            .filter_map(|q| q["reset_at"].as_i64())
            .max()
            .unwrap_or(at);
        self.0.execute_batch("SAVEPOINT routed_wait")?;
        let result = (|| -> Result<()> {
            self.wait(scope, turn, at, required)?;
            self.0.execute(
                "UPDATE waits SET reset_at=? WHERE scope=?",
                params![reset, scope],
            )?;
            self.0.execute("INSERT INTO wait_routes(scope,provider) VALUES (?,?) ON CONFLICT(scope) DO UPDATE SET provider=excluded.provider",params![scope,provider])?;
            Ok(())
        })();
        match result {
            Ok(()) => {
                self.0.execute_batch("RELEASE routed_wait")?;
                Ok(())
            }
            Err(e) => {
                let _ = self
                    .0
                    .execute_batch("ROLLBACK TO routed_wait; RELEASE routed_wait");
                Err(e)
            }
        }
    }
    pub fn provider_wait_ready(
        &self,
        scope: &str,
        at: i64,
        required: u64,
        released: bool,
    ) -> Result<bool> {
        let provider: Option<String> = self
            .0
            .query_row(
                "SELECT provider FROM wait_routes WHERE scope=?",
                [scope],
                |r| r.get(0),
            )
            .optional()?;
        let Some(provider) = provider else {
            return Ok(true);
        };
        let (_, p) = self.provider_policy(&provider)?;
        let q = self.provider_quota(&provider, at)?;
        Ok(q["active"] != true
            || ((p.auto_resume || released) && q["remaining"].as_u64().unwrap_or(0) >= required))
    }
}
