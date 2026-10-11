//! Lightweight machine status. Technical management belongs in Chat.
use crate::{
    design,
    localization::Catalog,
    pages::{self, Handler, Layout},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Disableable,
    button::{Button, ButtonVariants},
};
use serde_json::Value;

pub struct MachinesView<'a> {
    pub directory: &'a Value,
    pub preferred: &'a Value,
    /// A desktop transport is currently available only for this Runtime.
    pub desktop_runtime: Option<&'a str>,
}
pub fn render(
    view: MachinesView<'_>,
    layout: Layout,
    catalog: Catalog,
    handler: Handler,
    cx: &mut App,
) -> Div {
    let machines = view.directory["machines"]
        .as_array()
        .cloned()
        .unwrap_or_default();
    let selected = crate::machine_state::selection(view.directory, view.preferred);
    let mut body = div()
        .id("machine-status-list")
        .flex_1()
        .min_h_0()
        .overflow_y_scroll()
        .p_4()
        .flex()
        .flex_col()
        .gap_4();
    if machines.is_empty() {
        body = body.child(pages::hint(catalog.text("machines.loading"), cx));
    }
    for machine in machines {
        let runtime = machine["runtime_id"].as_str().unwrap_or_default();
        let online = machine["connection"] == "online";
        let name = machine["name"]
            .as_str()
            .unwrap_or(if machine["local"] == true {
                catalog.text("machines.local")
            } else {
                runtime
            });
        let mut card = div()
            .p_4()
            .rounded_lg()
            .bg(design::navigation(cx))
            .flex()
            .flex_col()
            .gap_3()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(name.to_owned())
                    .child(pages::hint(
                        catalog.text(if online {
                            "machines.online"
                        } else {
                            "machines.offline"
                        }),
                        cx,
                    )),
            );
        let environments = machine["environments"]
            .as_array()
            .cloned()
            .unwrap_or_default();
        if environments.is_empty() {
            card = card.child(pages::hint(catalog.text("machines.empty"), cx));
        }
        for environment in environments {
            let reference = &environment["environment_ref"];
            let host = reference["target"]["kind"] == "host";
            let title = if host {
                "Host"
            } else {
                environment["name"].as_str().unwrap_or("Sandbox")
            };
            let expanded = view.preferred.get("target").is_some() && selected == *reference;
            let phase = environment["state"]["phase"].as_str().unwrap_or("unknown");
            let status = if !online {
                catalog.text("machines.offline")
            } else if host {
                catalog.text("machines.online")
            } else {
                catalog.phase(phase)
            };
            let mut row = div().flex().flex_col().gap_2().child(
                Button::new(SharedString::from(format!(
                    "environment-{runtime}-{}",
                    reference["target"]
                )))
                .ghost()
                .w_full()
                .h_auto()
                .label(format!("{title} · {status}"))
                .on_click(pages::on(
                    &handler,
                    "machine-select",
                    if expanded {
                        Value::Null
                    } else {
                        reference.clone()
                    },
                )),
            );
            if expanded {
                let desktop = crate::machine_state::desktop_available(
                    &machine,
                    &environment,
                    view.desktop_runtime,
                );
                row = row.child(
                    div()
                        .px_3()
                        .pb_3()
                        .flex()
                        .flex_col()
                        .gap_3()
                        .child(pages::hint(
                            if online {
                                format!("{name} · {status}")
                            } else {
                                catalog.text("machines.offline-detail").into()
                            },
                            cx,
                        ))
                        .child(
                            div()
                                .flex()
                                .flex_wrap()
                                .gap_2()
                                .child(
                                    Button::new(SharedString::from(format!(
                                        "environment-tasks-{runtime}-{}",
                                        reference["target"]
                                    )))
                                    .ghost()
                                    .label(catalog.text("machines.view-tasks"))
                                    .on_click(pages::on(
                                        &handler,
                                        "environment-tasks",
                                        reference.clone(),
                                    )),
                                )
                                .when(desktop, |el| {
                                    el.child(
                                        Button::new(SharedString::from(format!(
                                            "environment-desktop-{runtime}-{}",
                                            reference["target"]
                                        )))
                                        .ghost()
                                        .label(catalog.text("machines.open-desktop"))
                                        .disabled(!online || phase != "running")
                                        .on_click(
                                            pages::on(
                                                &handler,
                                                "environment-desktop",
                                                reference.clone(),
                                            ),
                                        ),
                                    )
                                }),
                        ),
                );
            }
            card = card.child(row);
        }
        body = body.child(card);
    }
    pages::page(layout, cx).flex().flex_col().child(body)
}
