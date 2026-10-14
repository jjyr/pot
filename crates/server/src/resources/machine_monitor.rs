//! Page-owned interest; asynchronous checks publish through the normal change bus.
use super::*;
use futures_util::StreamExt;

#[derive(Default)]
pub(super) struct Monitor {
    users: usize,
    pub(super) task: Option<tokio::task::JoinHandle<()>>,
}
struct Interest(std::sync::Weak<Resources>);
impl Drop for Interest {
    fn drop(&mut self) {
        if let Some(resources) = self.0.upgrade() {
            let mut monitor = resources.monitor.lock().unwrap();
            monitor.users -= 1;
            if monitor.users == 0 {
                if let Some(task) = monitor.task.take() {
                    task.abort();
                }
            }
        }
    }
}
impl Resources {
    fn watch_machines(self: &Arc<Self>) -> Interest {
        let mut monitor = self.monitor.lock().unwrap();
        monitor.users += 1;
        if monitor.task.is_none() {
            let weak = Arc::downgrade(self);
            monitor.task = Some(tokio::spawn(async move {
                let mut previous = Value::Null;
                loop {
                    let Some(resources) = weak.upgrade() else {
                        break;
                    };
                    let host = resources.host().await;
                    let state = tokio::time::timeout(std::time::Duration::from_secs(10), async {
                        let (local, remote) = tokio::join!(
                            host.execution_target_inventory(),
                            resources.control.machines()
                        );
                        let mut state = json!({"local":local,"remote":remote});
                        strip_observation_times(&mut state);
                        state
                    })
                    .await;
                    if let Ok(state) = state {
                        if state != previous {
                            previous = state;
                            resources.changes.notify("machines");
                        }
                    }
                    drop(resources);
                    tokio::time::sleep(std::time::Duration::from_secs(3)).await;
                }
            }));
        }
        Interest(Arc::downgrade(self))
    }
}

// A streaming subscription owns interest, including during disconnect and token
// revocation. The response forwards the existing bus; it never drives UI state.
pub(super) async fn watch(State(resources): State<Arc<Resources>>, headers: HeaderMap) -> Response {
    if !resources.authorize(&headers) {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    let host = resources.host().await;
    let response = match pot_control::changes::events(State(host), headers).await {
        Ok(response) => response.into_response(),
        Err(error) => return error.into_response(),
    };
    let interest = resources.watch_machines();
    let (parts, body) = response.into_parts();
    let mut stream = body.into_data_stream();
    let guarded = async_stream::stream! {
        let _interest = interest;
        while let Some(chunk) = stream.next().await { yield chunk; }
    };
    Response::from_parts(parts, axum::body::Body::from_stream(guarded))
}

fn strip_observation_times(value: &mut Value) {
    match value {
        Value::Object(object) => {
            object.remove("last_seen_ms");
            for value in object.values_mut() {
                strip_observation_times(value);
            }
        }
        Value::Array(items) => {
            for value in items {
                strip_observation_times(value);
            }
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn checks_exist_only_while_authorized_pages_subscribe() {
        let root = std::env::temp_dir().join(format!("pot-machine-watch-{}", uuid::Uuid::new_v4()));
        let config = RuntimeConfig {
            data: root.join("product"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let resources = Resources::open(backend, config).await.unwrap();
        let router = resources.router();
        let request = |path: &str, token: &str| {
            axum::http::Request::builder()
                .uri(path)
                .header("authorization", format!("Bearer {token}"))
                .body(axum::body::Body::empty())
                .unwrap()
        };
        let ordinary = router
            .clone()
            .oneshot(request("/api/v1/changes", &resources.token))
            .await
            .unwrap();
        assert!(resources.monitor.lock().unwrap().task.is_none());
        let rejected = router
            .clone()
            .oneshot(request("/api/v1/machines/watch", "invalid"))
            .await
            .unwrap();
        assert_eq!(rejected.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(resources.monitor.lock().unwrap().users, 0);
        let a = router
            .clone()
            .oneshot(request("/api/v1/machines/watch", &resources.token))
            .await
            .unwrap();
        let b = router
            .clone()
            .oneshot(request("/api/v1/machines/watch", &resources.token))
            .await
            .unwrap();
        assert_eq!(a.status(), StatusCode::OK);
        assert_eq!(resources.monitor.lock().unwrap().users, 2);
        let task_id = resources
            .monitor
            .lock()
            .unwrap()
            .task
            .as_ref()
            .unwrap()
            .id();
        let mut stream = ordinary.into_body().into_data_stream();
        let mut decoder = pot_protocol::sse::Decoder::default();
        tokio::time::timeout(std::time::Duration::from_secs(1), async {
            loop {
                let chunk = stream.next().await.unwrap().unwrap();
                for frame in decoder.push(&chunk).unwrap() {
                    if serde_json::from_str::<Value>(&frame.data).unwrap()["versions"]["machines"]
                        .is_number()
                    {
                        return;
                    }
                }
            }
        })
        .await
        .unwrap();
        // Even dropping an unread response releases its interest.
        drop(a);
        assert_eq!(resources.monitor.lock().unwrap().users, 1);
        assert_eq!(
            resources
                .monitor
                .lock()
                .unwrap()
                .task
                .as_ref()
                .unwrap()
                .id(),
            task_id
        );
        drop(b);
        assert_eq!(resources.monitor.lock().unwrap().users, 0);
        assert!(resources.monitor.lock().unwrap().task.is_none());
        let c = router
            .clone()
            .oneshot(request("/api/v1/machines/watch", &resources.token))
            .await
            .unwrap();
        assert_ne!(
            resources
                .monitor
                .lock()
                .unwrap()
                .task
                .as_ref()
                .unwrap()
                .id(),
            task_id
        );
        drop(c);
        assert!(resources.monitor.lock().unwrap().task.is_none());
        resources.shutdown().await;
        drop(stream);
        drop(router);
        drop(resources);
        std::fs::remove_dir_all(root).unwrap();
    }
}
