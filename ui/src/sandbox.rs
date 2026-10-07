use crate::pages::tr;
use crate::{
    design,
    localization::Catalog,
    pages::{self, Handler, Layout, *},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};
pub const SANDBOX_LIST: f32 = 224.;
pub fn render(
    state: &Value,
    layout: Layout,
    controls: bool,
    catalog: Catalog,
    handler: Handler,
    cx: &mut App,
) -> Div {
    let selected = state["sandbox_view"].as_str().unwrap_or("default");
    let default = state["sandbox_default"].as_str().unwrap_or("default");
    let items = state["sandboxes"].as_array().cloned().unwrap_or_default();
    let current = items.iter().find(|item| item["id"] == selected);
    let name = current
        .and_then(|item| item["name"].as_str())
        .unwrap_or("Sandbox");
    let phase = state["phase"].as_str().unwrap_or("unknown");
    let connected = phase == "running" && state["viewer"]["connected"] == true;
    let human = state["viewer"]["human"] == true;
    let pending = state["sandbox_busy"] == true;
    let mut list = div()
        .id("sandbox-list")
        .flex_1()
        .min_h_0()
        .overflow_y_scroll()
        .p_3()
        .flex()
        .flex_col()
        .gap_2();
    for item in items.iter() {
        let id = item["id"].as_str().unwrap_or("").to_owned();
        let target = id.clone();
        let active = id == selected;
        let row = div()
            .w_full()
            .min_w_0()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .font_weight(FontWeight::MEDIUM)
                    .overflow_hidden()
                    .child(item["name"].as_str().unwrap_or("Linux").to_owned()),
            )
            .child(hint(
                tr!(
                    catalog,
                    "ui.running-tasks",
                    catalog.phase(item["state"]["phase"].as_str().unwrap_or("unknown")),
                    item["running_tasks"].as_u64().unwrap_or(0)
                ),
                cx,
            ))
            .when(id == default, |row| {
                row.child(hint(catalog.text("ui.execution-default"), cx))
            });
        let row = Button::new(SharedString::from(format!("sandbox-{id}")))
            .ghost()
            .w_full()
            .h_auto()
            .p_3()
            .bg(if active {
                cx.theme().secondary
            } else {
                design::navigation(cx)
            })
            .accessibility_label(tr!(
                catalog,
                "ui.select-sandbox",
                item["name"].as_str().unwrap_or("Linux")
            ))
            .child(row)
            .on_click(pages::on(&handler, "sandbox-select", json!(target)));
        list = list.child(row);
    }
    let sidebar = div()
        .w(if layout.compact {
            relative(1.)
        } else {
            px(SANDBOX_LIST).into()
        })
        .h(if layout.compact {
            px(260.).into()
        } else {
            relative(1.)
        })
        .flex_shrink_0()
        .bg(design::navigation(cx))
        .border_r_1()
        .border_color(cx.theme().border)
        .flex()
        .flex_col()
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_4()
                .flex()
                .items_center()
                .justify_between()
                .child("Sandbox")
                .child(
                    Button::new("sandbox-new")
                        .ghost()
                        .small()
                        .label(catalog.text("ui.new"))
                        .disabled(pending || !controls)
                        .on_click(pages::on(
                            &handler,
                            "native",
                            json!({"action":"sandbox-new"}),
                        )),
                ),
        )
        .child(list)
        .when(
            state["sandbox_error"]
                .as_str()
                .is_some_and(|e| !e.is_empty()),
            |el| {
                el.child(
                    div()
                        .p_3()
                        .text_color(design::attention(cx))
                        .child(catalog.message(state["sandbox_error"].as_str().unwrap_or(""))),
                )
            },
        );
    let desktop = div()
        .flex_1()
        .min_w_0()
        .h_full()
        .flex()
        .flex_col()
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_6()
                .flex()
                .items_center()
                .gap_3()
                .border_b_1()
                .border_color(cx.theme().border)
                .child(
                    div()
                        .flex_1()
                        .overflow_hidden()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(name.to_owned()),
                )
                .child(hint(catalog.phase(phase), cx))
                .when(selected == default, |el| {
                    el.child(hint(catalog.text("ui.execution-default"), cx))
                }),
        )
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_6()
                .flex()
                .items_center()
                .justify_between()
                .border_b_1()
                .border_color(cx.theme().border)
                .child(hint(
                    if pending {
                        catalog.text("ui.working")
                    } else if !connected {
                        catalog.text("ui.desktop-disconnected")
                    } else if human {
                        catalog.text("ui.you-control-the-desktop")
                    } else {
                        catalog.text("ui.agent-controls-the-desktop")
                    },
                    cx,
                ))
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            Button::new("take")
                                .primary()
                                .small()
                                .label(if human {
                                    catalog.text("ui.give-back-control")
                                } else {
                                    catalog.text("ui.take-over")
                                })
                                .disabled(!connected || pending || !controls)
                                .on_click(pages::on(
                                    &handler,
                                    "native",
                                    json!({"action":"viewer","id":"take"}),
                                )),
                        )
                        .child(
                            Button::new("sandbox-settings")
                                .ghost()
                                .small()
                                .icon(IconName::Settings)
                                .tooltip(catalog.text("ui.environment-settings"))
                                .accessibility_label(catalog.text("ui.environment-settings"))
                                .disabled(pending || !controls)
                                .on_click(pages::on(
                                    &handler,
                                    "native",
                                    json!({"action":"sandbox-settings"}),
                                )),
                        )
                        .child(
                            Button::new("sandbox-more")
                                .ghost()
                                .small()
                                .icon(IconName::Ellipsis)
                                .tooltip(catalog.text("ui.more-actions"))
                                .accessibility_label(catalog.text("ui.more-actions"))
                                .disabled(pending || !controls)
                                .on_click(pages::on(
                                    &handler,
                                    "native",
                                    json!({"action":"viewer","id":"more"}),
                                )),
                        ),
                ),
        )
        .when(!connected, |el| {
            el.child(
                div()
                    .flex_1()
                    .p_8()
                    .flex()
                    .flex_col()
                    .items_center()
                    .justify_center()
                    .gap_4()
                    .child(hint(
                        if !controls {
                            catalog.text("mobile.desktop-access-awaits-permission-qualification")
                        } else if phase == "running" {
                            catalog.text("ui.connecting-to-desktop")
                        } else {
                            catalog.text("ui.this-environment-is-stopped-start-the-desktop")
                        },
                        cx,
                    ))
                    .child(
                        Button::new("start-sandbox")
                            .primary()
                            .label(catalog.text("ui.start-desktop"))
                            .disabled(
                                !controls
                                    || pending
                                    || !matches!(phase, "running" | "stopped" | "failed"),
                            )
                            .on_click(pages::on(&handler, "native", json!({"action":"start"}))),
                    ),
            )
        });
    page(layout, cx)
        .flex()
        .when(layout.compact, |el| el.flex_col())
        .child(sidebar)
        .child(desktop)
}
