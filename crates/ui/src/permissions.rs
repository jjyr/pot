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
pub fn cards(
    book: &Value,
    directory: &Value,
    owner: &str,
    catalog: Catalog,
    handler: Handler,
    cx: &mut App,
) -> Div {
    let mut result = div().flex().flex_col().gap_2();
    for record in book["requests"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|p| book["assistant_id"] == owner || p["request"]["owner"] == owner)
    {
        let mut display = record.clone();
        if let Some(machine) = directory["machines"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|m| m["runtime_id"] == record["scope"]["machine_id"])
        {
            display["machine_name"] = machine["name"].clone();
        }
        let id = record["id"].as_str().unwrap_or("");
        let card = pages::card(cx)
            .child(pages::title(catalog.text("permissions.request")))
            .child(
                div()
                    .text_sm()
                    .whitespace_normal()
                    .child(crate::permission_display::description(&display, catalog)),
            );
        let mut actions = div().flex().flex_wrap().gap_2();
        for action in ["allow_once", "deny"] {
            actions = actions.child(
                Button::new(SharedString::from(format!("permission-{id}-{action}")))
                    .ghost()
                    .small()
                    .label(catalog.text(
                        if action == "allow_once" && record["scope"]["capability"] == "host" {
                            "permissions.allow-operation"
                        } else {
                            match action {
                                "allow_once" => "permissions.allow_once",
                                _ => "permissions.deny",
                            }
                        },
                    ))
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"id":id,"action":action}),
                    )),
            );
        }
        result = result.child(card.child(actions));
    }
    result
}
