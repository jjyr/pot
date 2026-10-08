//! Owner approval cards. Decisions are explicit clicks, never transcript text.
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
pub fn cards(book: &Value, owner: &str, catalog: Catalog, handler: Handler, cx: &mut App) -> Div {
    let mut result = div().flex().flex_col().gap_2();
    for record in book["requests"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|p| owner == "main" || p["request"]["owner"] == owner)
    {
        let request = &record["request"];
        let id = record["id"].as_str().unwrap_or("");
        let mut card = pages::card(cx)
            .child(pages::title(catalog.text("permissions.request")))
            .child(pages::hint(
                format!(
                    "{} · {} · {}",
                    request["owner"].as_str().unwrap_or(""),
                    request["environment"].as_str().unwrap_or("host"),
                    request["tool"].as_str().unwrap_or("")
                ),
                cx,
            ))
            .child(
                div()
                    .text_sm()
                    .whitespace_normal()
                    .child(serde_json::to_string_pretty(&request["args"]).unwrap_or_default()),
            )
            .child(pages::hint(catalog.text("permissions.scope"), cx))
            .child(div().text_sm().whitespace_normal().child(
                serde_json::to_string_pretty(&request["suggested_scope"]).unwrap_or_default(),
            ));
        if request["tool"] == "exec_command" && request["environment"] == "host" {
            card = card.child(pages::hint(catalog.text("permissions.shell-effects"), cx));
        }
        let mut actions = div().flex().flex_wrap().gap_2();
        for action in ["allow_persistent", "allow_once", "deny"] {
            actions = actions.child(
                Button::new(SharedString::from(format!("permission-{id}-{action}")))
                    .ghost()
                    .small()
                    .label(catalog.text(&format!("permissions.{action}")))
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"id":id,"action":action}),
                    )),
            );
        }
        result = result.child(card.child(actions));
    }
    for grant in book["grants"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|g| owner == "main" || g["owner"] == owner)
    {
        let id = grant["id"].as_str().unwrap_or("");
        result = result.child(
            pages::card(cx)
                .child(pages::title(catalog.text("permissions.grant")))
                .child(pages::hint(
                    format!(
                        "{} · {}",
                        grant["owner"].as_str().unwrap_or(""),
                        grant["environment"].as_str().unwrap_or("host")
                    ),
                    cx,
                ))
                .child(
                    div()
                        .text_sm()
                        .whitespace_normal()
                        .child(serde_json::to_string_pretty(&grant["scope"]).unwrap_or_default()),
                )
                .child(
                    Button::new(SharedString::from(format!("permission-revoke-{id}")))
                        .ghost()
                        .small()
                        .label(catalog.text("permissions.revoke"))
                        .on_click(pages::on(
                            &handler,
                            "permission-decision",
                            json!({"id":id,"action":"revoke"}),
                        )),
                ),
        );
    }
    result
}
