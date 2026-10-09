//! Product-generated resource permission cards shared by Chat and Sandbox.
use crate::{
    localization::Catalog,
    pages::{self, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Sizable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};
pub fn cards(requests: &Value, catalog: Catalog, handler: Handler, cx: &mut App) -> Div {
    let mut cards = div().flex().flex_col().gap_2();
    for request in requests
        .as_array()
        .into_iter()
        .flatten()
        .filter(|r| !matches!(r["phase"].as_str(), Some("ready" | "cancelled")))
    {
        let id = request["id"].as_str().unwrap_or("");
        let phase = request["phase"].as_str().unwrap_or("");
        let p = &request["progress"];
        let downloaded = p["downloaded_bytes"].as_u64().unwrap_or(0);
        let total = p["total_bytes"]
            .as_u64()
            .unwrap_or(request["missing_bytes"].as_u64().unwrap_or(0));
        let mib = |bytes: u64| format!("{:.1} MiB", bytes as f64 / 1048576.);
        let mut card = pages::card(cx)
            .child(pages::title(catalog.text("resources.title")))
            .child(pages::hint(
                format!(
                    "{} · {}",
                    request["sandbox_id"].as_str().unwrap_or(""),
                    request["bundle_id"].as_str().unwrap_or("")
                ),
                cx,
            ))
            .child(pages::hint(catalog.text(&format!("resources.{phase}")), cx))
            .child(pages::hint(
                format!("{} / {}", mib(downloaded), mib(total)),
                cx,
            ));
        if let Some(resource) = request["resource"].as_str().filter(|s| !s.is_empty()) {
            card = card.child(pages::hint(resource.to_owned(), cx));
        }
        if p["bytes_per_second"].as_u64().unwrap_or(0) > 0 {
            card = card.child(pages::hint(
                format!(
                    "{}/s · {} s",
                    mib(p["bytes_per_second"].as_u64().unwrap_or(0)),
                    p["remaining_seconds"].as_u64().unwrap_or(0)
                ),
                cx,
            ));
        }
        if total > 0 && phase == "downloading" {
            card = card.child(
                div().h(px(4.)).w_full().bg(rgb(0xe8e2db)).child(
                    div()
                        .h_full()
                        .w(relative((downloaded as f32 / total as f32).min(1.)))
                        .bg(rgb(0xaa5c44)),
                ),
            );
        }
        if let Some(error) = request["error"].as_str().filter(|s| !s.is_empty()) {
            card = card.child(pages::hint(error.to_owned(), cx));
        }
        let mut actions = div().flex().gap_2();
        if matches!(phase, "awaiting-authorization" | "interrupted" | "failed") {
            let action = if phase == "awaiting-authorization" {
                "authorize"
            } else if phase == "failed" {
                "retry"
            } else {
                "continue"
            };
            actions = actions.child(
                Button::new(SharedString::from(format!("resource-{id}-{action}")))
                    .primary()
                    .small()
                    .label(catalog.text(&format!("resources.{action}")))
                    .on_click(pages::on(
                        &handler,
                        "native",
                        json!({"action":"resource-command","request_id":id,"operation":action}),
                    )),
            );
        }
        actions = actions.child(
            Button::new(SharedString::from(format!("resource-{id}-cancel")))
                .ghost()
                .small()
                .label(catalog.text("resources.cancel"))
                .on_click(pages::on(
                    &handler,
                    "native",
                    json!({"action":"resource-command","request_id":id,"operation":"cancel"}),
                )),
        );
        cards = cards.child(card.child(actions));
    }
    cards
}
