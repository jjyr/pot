use pot_eval::records::*;
fn report(id: &str) -> UsageReport {
    UsageReport {
        measurement_id: id.into(),
        source_event_id: "event-1".into(),
        scope: Scope::Request,
        source: "provider".into(),
        counter: Counter::Snapshot,
        complete: true,
        input_tokens: Some(100),
        output_tokens: Some(20),
        cache_read_tokens: Some(60),
        cache_write_tokens: None,
        reasoning_tokens: Some(5),
        baseline_input: None,
        baseline_output: None,
    }
}
#[test]
fn snapshots_duplicates_sources_and_parent_totals_do_not_add() {
    let a = report("request");
    let mut b = a.clone();
    b.source_event_id = "event-2".into();
    b.input_tokens = Some(120);
    let mut native = b.clone();
    native.source = "native".into();
    let mut parent = b.clone();
    parent.scope = Scope::Turn;
    parent.measurement_id = "turn".into();
    let total = aggregate(&[a, b.clone(), b, native, parent], Scope::Request).unwrap();
    assert_eq!(
        total,
        Totals {
            input_tokens: Some(120),
            output_tokens: Some(20),
            cache_read_tokens: Some(60),
            cache_write_tokens: None,
            reasoning_tokens: Some(5),
            measurements: 1
        }
    );
}
#[test]
fn unknown_and_partial_remain_unknown_and_zero_is_valid() {
    let mut a = report("a");
    a.complete = false;
    let total = aggregate(&[a, report("b")], Scope::Request).unwrap();
    assert_eq!(total.input_tokens, None);
    let mut zero = report("zero");
    zero.input_tokens = Some(0);
    zero.output_tokens = Some(0);
    zero.cache_read_tokens = None;
    zero.reasoning_tokens = None;
    assert_eq!(
        aggregate(&[zero], Scope::Request).unwrap().input_tokens,
        Some(0)
    );
    assert_eq!(aggregate(&[], Scope::Request).unwrap().input_tokens, None);
}
#[test]
fn cumulative_requires_baseline_and_rejects_reset() {
    let mut a = report("a");
    a.counter = Counter::Cumulative;
    assert_eq!(
        aggregate(&[a.clone()], Scope::Request)
            .unwrap()
            .input_tokens,
        None
    );
    a.baseline_input = Some(80);
    a.baseline_output = Some(10);
    assert_eq!(
        aggregate(&[a.clone()], Scope::Request)
            .unwrap()
            .input_tokens,
        Some(20)
    );
    a.baseline_input = Some(101);
    assert!(aggregate(&[a], Scope::Request).is_err());
}
#[test]
fn mismatches_subsets_counter_changes_and_estimates_are_rejected() {
    let a = report("a");
    let mut b = a.clone();
    b.source = "native".into();
    b.input_tokens = Some(101);
    assert!(aggregate(&[a.clone(), b], Scope::Request).is_err());
    let mut b = a.clone();
    b.cache_read_tokens = Some(101);
    assert!(aggregate(&[b], Scope::Request).is_err());
    let mut b = a.clone();
    b.source = "estimate".into();
    assert!(aggregate(&[b], Scope::Request).is_err());
    let mut b = a.clone();
    b.input_tokens = Some(101);
    assert!(aggregate(&[a.clone(), b], Scope::Request).is_err());
    let mut b = a.clone();
    b.source_event_id = "event-2".into();
    b.counter = Counter::Delta;
    assert!(aggregate(&[a, b], Scope::Request).is_err());
}
#[test]
fn deltas_deduplicate_and_overflow_is_rejected() {
    let mut a = report("a");
    a.counter = Counter::Delta;
    let mut b = a.clone();
    b.source_event_id = "event-2".into();
    assert_eq!(
        aggregate(&[a.clone(), a.clone(), b], Scope::Request)
            .unwrap()
            .input_tokens,
        Some(200)
    );
    a.input_tokens = Some(u64::MAX);
    assert!(aggregate(&[a, report("b")], Scope::Request).is_err());
}

#[test]
fn cumulative_cannot_hide_a_reset_above_the_baseline() {
    let mut a = report("a");
    a.counter = Counter::Cumulative;
    a.baseline_input = Some(50);
    a.baseline_output = Some(10);
    let mut b = a.clone();
    b.input_tokens = Some(80);
    b.source_event_id = "event-2".into();
    assert!(aggregate(&[a.clone(), b], Scope::Request).is_err());
    let mut b = a.clone();
    b.baseline_input = Some(40);
    b.source_event_id = "event-2".into();
    assert!(aggregate(&[a, b], Scope::Request).is_err());
}
