//! Operator-only UI invalidations. Versions coalesce; clients fetch dirty projections.
use crate::*;
use futures::StreamExt;
pub async fn events(
    State(host): State<host::Host>,
    headers: HeaderMap,
) -> ApiResult<impl IntoResponse> {
    host.authorize(&headers)?;
    let mut changes = host.changes.subscribe();
    let identity = host.describe();
    let mut stop = host.stop_receiver();
    let stream = async_stream::stream! {
        loop {
            if *stop.borrow() { break; }
            let versions = tokio::select! { _ = stop.changed() => break, next = changes.next() => match next { Some(v) => v, None => break } };
            yield Ok::<_, Infallible>(SseEvent::default().event("changes").json_data(json!({
                "runtime_id":identity["runtime_id"], "boot_id":identity["boot_id"], "versions":versions
            })).unwrap());
        }
    };
    Ok(Sse::new(stream)
        .keep_alive(KeepAlive::default().interval(std::time::Duration::from_secs(5))))
}
