use anyhow::{Result, ensure};
use pot_core::{DriverDescription, DriverEvent, ExecutionObservation, ResolvedSpec};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const SCHEMA: u32 = 1;
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Manifest {
    pub schema_version: u32,
    pub run_id: String,
    pub scenario_id: String,
    pub scenario_hash: String,
    pub mode: String,
    pub driver: String,
    pub build_version: String,
    pub runtime_revision: Option<String>,
    pub effective_spec: ResolvedSpec,
    pub capabilities: DriverDescription,
    pub deadline_ms: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub transport: Option<serde_json::Value>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Event {
    pub schema_version: u32,
    pub run_id: String,
    pub seq: u64,
    pub elapsed_ms: u64,
    pub turn_id: String,
    pub payload: Payload,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", content = "data", rename_all = "snake_case")]
pub enum Payload {
    Driver(DriverEvent),
    Observation(ExecutionObservation),
    Usage(UsageReport),
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum Scope {
    Request,
    Turn,
    Run,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Counter {
    Snapshot,
    Cumulative,
    Delta,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct UsageReport {
    pub measurement_id: String,
    pub source_event_id: String,
    pub scope: Scope,
    pub source: String,
    pub counter: Counter,
    pub complete: bool,
    pub input_tokens: Option<u64>,
    pub output_tokens: Option<u64>,
    pub cache_read_tokens: Option<u64>,
    pub cache_write_tokens: Option<u64>,
    pub reasoning_tokens: Option<u64>,
    pub baseline_input: Option<u64>,
    pub baseline_output: Option<u64>,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct Totals {
    pub input_tokens: Option<u64>,
    pub output_tokens: Option<u64>,
    pub cache_read_tokens: Option<u64>,
    pub cache_write_tokens: Option<u64>,
    pub reasoning_tokens: Option<u64>,
    pub measurements: usize,
}
fn add(a: Option<u64>, b: Option<u64>) -> Result<Option<u64>> {
    match (a, b) {
        (Some(a), Some(b)) => Ok(Some(
            a.checked_add(b)
                .ok_or_else(|| anyhow::anyhow!("Token overflow"))?,
        )),
        _ => Ok(None),
    }
}
fn checked(report: &UsageReport) -> Result<Totals> {
    if let Some(input) = report.input_tokens {
        for part in [report.cache_read_tokens, report.cache_write_tokens]
            .into_iter()
            .flatten()
        {
            ensure!(part <= input, "Cache subset exceeds input");
        }
    }
    if let (Some(reasoning), Some(output)) = (report.reasoning_tokens, report.output_tokens) {
        ensure!(reasoning <= output, "Reasoning subset exceeds output");
    }
    let mut result = Totals {
        input_tokens: report.input_tokens,
        output_tokens: report.output_tokens,
        cache_read_tokens: report.cache_read_tokens,
        cache_write_tokens: report.cache_write_tokens,
        reasoning_tokens: report.reasoning_tokens,
        measurements: 1,
    };
    if report.counter == Counter::Cumulative {
        result.cache_read_tokens = None;
        result.cache_write_tokens = None;
        result.reasoning_tokens = None;
        result.input_tokens = match (report.input_tokens, report.baseline_input) {
            (Some(end), Some(start)) => Some(
                end.checked_sub(start)
                    .ok_or_else(|| anyhow::anyhow!("Cumulative input reset"))?,
            ),
            _ => None,
        };
        result.output_tokens = match (report.output_tokens, report.baseline_output) {
            (Some(end), Some(start)) => Some(
                end.checked_sub(start)
                    .ok_or_else(|| anyhow::anyhow!("Cumulative output reset"))?,
            ),
            _ => None,
        };
    }
    if !report.complete {
        result.input_tokens = None;
        result.output_tokens = None;
        result.cache_read_tokens = None;
        result.cache_write_tokens = None;
        result.reasoning_tokens = None;
    }
    Ok(result)
}
/// Explicit scope prevents adding parent totals to their child measurements.
/// Sources for the same measurement must reconcile; they are never summed.
pub fn aggregate(reports: &[UsageReport], scope: Scope) -> Result<Totals> {
    struct SourceState {
        counter: Counter,
        total: Totals,
        events: BTreeMap<String, String>,
        cumulative: Option<(Option<u64>, Option<u64>, Option<u64>, Option<u64>)>,
    }
    let mut sources: BTreeMap<(String, String), SourceState> = BTreeMap::new();
    for report in reports.iter().filter(|r| r.scope == scope) {
        ensure!(
            matches!(report.source.as_str(), "provider" | "native"),
            "Estimates/reservations are not observed usage"
        );
        ensure!(
            !report.measurement_id.is_empty() && !report.source_event_id.is_empty(),
            "Missing usage identity"
        );
        let value = checked(report)?;
        let entry = sources
            .entry((report.measurement_id.clone(), report.source.clone()))
            .or_insert_with(|| SourceState {
                counter: report.counter.clone(),
                total: Totals::default(),
                events: BTreeMap::new(),
                cumulative: None,
            });
        ensure!(
            entry.counter == report.counter,
            "Counter semantics changed within measurement"
        );
        let serialized = serde_json::to_string(report)?;
        if let Some(previous) = entry.events.get(&report.source_event_id) {
            ensure!(previous == &serialized, "Conflicting duplicate usage event");
            continue;
        }
        entry
            .events
            .insert(report.source_event_id.clone(), serialized);
        if report.counter == Counter::Cumulative {
            if let Some((input, output, baseline_input, baseline_output)) = entry.cumulative {
                ensure!(
                    baseline_input == report.baseline_input
                        && baseline_output == report.baseline_output,
                    "Cumulative baseline changed"
                );
                for (previous, current) in
                    [(input, report.input_tokens), (output, report.output_tokens)]
                {
                    ensure!(
                        previous.zip(current).is_none_or(|(a, b)| b >= a),
                        "Cumulative counter reset"
                    );
                }
            }
            entry.cumulative = Some((
                report
                    .input_tokens
                    .or_else(|| entry.cumulative.and_then(|v| v.0)),
                report
                    .output_tokens
                    .or_else(|| entry.cumulative.and_then(|v| v.1)),
                report.baseline_input,
                report.baseline_output,
            ));
        }
        if report.counter == Counter::Delta && entry.total.measurements != 0 {
            entry.total.input_tokens = add(entry.total.input_tokens, value.input_tokens)?;
            entry.total.output_tokens = add(entry.total.output_tokens, value.output_tokens)?;
            entry.total.cache_read_tokens =
                add(entry.total.cache_read_tokens, value.cache_read_tokens)?;
            entry.total.cache_write_tokens =
                add(entry.total.cache_write_tokens, value.cache_write_tokens)?;
            entry.total.reasoning_tokens =
                add(entry.total.reasoning_tokens, value.reasoning_tokens)?;
        } else {
            entry.total = value;
        }
    }
    let mut unique: BTreeMap<String, Totals> = BTreeMap::new();
    for ((id, _), state) in sources {
        let value = state.total;
        if let Some(previous) = unique.get_mut(&id) {
            for (a, b) in [
                (previous.input_tokens, value.input_tokens),
                (previous.output_tokens, value.output_tokens),
                (previous.cache_read_tokens, value.cache_read_tokens),
                (previous.cache_write_tokens, value.cache_write_tokens),
                (previous.reasoning_tokens, value.reasoning_tokens),
            ] {
                ensure!(
                    a.is_none() || b.is_none() || a == b,
                    "Usage sources disagree for {id}"
                );
            }
            previous.input_tokens = previous.input_tokens.or(value.input_tokens);
            previous.output_tokens = previous.output_tokens.or(value.output_tokens);
            previous.cache_read_tokens = previous.cache_read_tokens.or(value.cache_read_tokens);
            previous.cache_write_tokens = previous.cache_write_tokens.or(value.cache_write_tokens);
            previous.reasoning_tokens = previous.reasoning_tokens.or(value.reasoning_tokens);
        } else {
            unique.insert(id, value);
        }
    }
    let mut total = Totals {
        input_tokens: Some(0),
        output_tokens: Some(0),
        cache_read_tokens: Some(0),
        cache_write_tokens: Some(0),
        reasoning_tokens: Some(0),
        measurements: unique.len(),
    };
    for value in unique.values() {
        total.input_tokens = add(total.input_tokens, value.input_tokens)?;
        total.output_tokens = add(total.output_tokens, value.output_tokens)?;
        total.cache_read_tokens = add(total.cache_read_tokens, value.cache_read_tokens)?;
        total.cache_write_tokens = add(total.cache_write_tokens, value.cache_write_tokens)?;
        total.reasoning_tokens = add(total.reasoning_tokens, value.reasoning_tokens)?;
    }
    if unique.is_empty() {
        total.input_tokens = None;
        total.output_tokens = None;
        total.cache_read_tokens = None;
        total.cache_write_tokens = None;
        total.reasoning_tokens = None;
    }
    Ok(total)
}
