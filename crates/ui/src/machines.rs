//! Shared machine/environment navigation; platform adapters own transport and effects.
use crate::{
    design,
    localization::Catalog,
    pages::{self, Handler, Layout},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};

pub struct MachinesView<'a> {
    pub directory: &'a Value,
    pub preferred: &'a Value,
    pub sandbox_state: &'a Value,
    pub controls: bool,
}
pub fn render(
    view: MachinesView<'_>,
    layout: Layout,
    catalog: Catalog,
    handler: Handler,
    cx: &mut App,
) -> Div {
    let MachinesView {
        directory,
        preferred,
        sandbox_state,
        controls,
    } = view;
    let selected = crate::machine_state::selection(directory, preferred);
    let machines = directory["machines"]
        .as_array()
        .cloned()
        .unwrap_or_default();
    let mut list = div()
        .id("machine-list")
        .flex_1()
        .min_h_0()
        .overflow_y_scroll()
        .p_3()
        .flex()
        .flex_col()
        .gap_2();
    for machine in &machines {
        let id = machine["runtime_id"].as_str().unwrap_or_default();
        let name = machine["name"]
            .as_str()
            .unwrap_or(if machine["local"] == true {
                catalog.text("machines.local")
            } else {
                id
            });
        let active = selected["runtime_id"] == id && selected.get("target").is_none();
        list = list.child(
            Button::new(SharedString::from(format!("machine-{id}")))
                .ghost()
                .w_full()
                .h_auto()
                .p_3()
                .bg(if active {
                    cx.theme().secondary
                } else {
                    design::navigation(cx)
                })
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .w_full()
                        .items_start()
                        .gap_2()
                        .child(name.to_owned())
                        .child(pages::hint(
                            catalog.text(if machine["connection"] == "online" {
                                "machines.online"
                            } else {
                                "machines.offline"
                            }),
                            cx,
                        )),
                )
                .on_click(pages::on(
                    &handler,
                    "machine-select",
                    json!({"runtime_id":id}),
                )),
        );
        let environments = machine["environments"]
            .as_array()
            .cloned()
            .unwrap_or_default();
        if environments.is_empty() {
            list = list.child(
                div()
                    .px_3()
                    .child(pages::hint(catalog.text("machines.empty"), cx)),
            );
        }
        for environment in environments {
            let reference = &environment["environment_ref"];
            let host = reference["target"]["kind"] == "host";
            let name = if host {
                "Host"
            } else {
                environment["name"].as_str().unwrap_or("Sandbox")
            };
            let state = if host {
                catalog.text("machines.approval")
            } else {
                catalog.phase(environment["state"]["phase"].as_str().unwrap_or("unknown"))
            };
            list = list.child(
                Button::new(SharedString::from(format!(
                    "environment-{id}-{}",
                    reference["target"]
                )))
                .ghost()
                .w_full()
                .h_auto()
                .p_3()
                .ml_2()
                .bg(if selected == *reference {
                    cx.theme().secondary
                } else {
                    design::navigation(cx)
                })
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .w_full()
                        .items_start()
                        .gap_2()
                        .child(name.to_owned())
                        .child(pages::hint(state, cx).whitespace_normal()),
                )
                .on_click(pages::on(
                    &handler,
                    "machine-select",
                    reference.clone(),
                )),
            );
        }
    }
    let sidebar = div()
        .w(if layout.compact {
            relative(1.)
        } else {
            px(224.).into()
        })
        .h(if layout.compact {
            px(260.).into()
        } else {
            relative(1.)
        })
        .flex_shrink_0()
        .flex()
        .flex_col()
        .bg(design::navigation(cx))
        .border_r_1()
        .border_color(cx.theme().border)
        .child(
            div()
                .h(px(56.))
                .px_4()
                .flex()
                .items_center()
                .child(catalog.text("machines.title")),
        )
        .child(list)
        .when(controls, |el| {
            el.child(
                Button::new("machines-resource-cache")
                    .ghost()
                    .label(catalog.text("resources.cache"))
                    .on_click(pages::on(
                        &handler,
                        "native",
                        json!({"action":"resource-cache"}),
                    )),
            )
        });
    let machine = machines
        .iter()
        .find(|m| m["runtime_id"] == selected["runtime_id"]);
    let mut detail = div().flex_1().min_w_0().h_full().flex().flex_col();
    if let Some(machine) = machine {
        let environment = machine["environments"]
            .as_array()
            .and_then(|items| items.iter().find(|e| e["environment_ref"] == selected));
        if let Some(environment) = environment {
            if selected["target"]["kind"] == "sandbox" && machine["local"] == true {
                let target = selected["target"]["sandbox_id"]
                    .as_str()
                    .unwrap_or_default();
                let mut state = sandbox_state.clone();
                state["machines_embedded"] = json!(true);
                state["sandbox_view"] = json!(target);
                state["sandbox_default"] = machine["default"].clone();
                state["sandboxes"] = machine["environments"].clone();
                state["phase"] = environment["state"]["phase"].clone();
                detail = crate::sandbox::render(
                    &state,
                    layout,
                    controls && machine["connection"] == "online",
                    catalog,
                    handler,
                    cx,
                );
            } else {
                detail = detail.child(
                    div()
                        .p_8()
                        .flex()
                        .flex_col()
                        .gap_4()
                        .child(pages::title(if selected["target"]["kind"] == "host" {
                            "Host"
                        } else {
                            "Sandbox"
                        }))
                        .child(pages::hint(
                            machine["runtime_id"]
                                .as_str()
                                .unwrap_or_default()
                                .to_owned(),
                            cx,
                        ))
                        .child(pages::hint(catalog.text("machines.approval"), cx))
                        .when_some(environment["workspace"].as_str(), |el, cwd| {
                            el.child(pages::hint(
                                format!("{}: {cwd}", catalog.text("machines.workspace")),
                                cx,
                            ))
                        })
                        .child(pages::hint(catalog.text("machines.sessions"), cx))
                        .children(
                            environment["sessions"]
                                .as_array()
                                .cloned()
                                .unwrap_or_default()
                                .into_iter()
                                .map(|scope| {
                                    let processes = scope["sessions"]["processes"]["sessions"]
                                        .as_array()
                                        .map(Vec::len)
                                        .unwrap_or(0);
                                    let plugins = scope["sessions"]["plugins"]
                                        .as_array()
                                        .map(Vec::len)
                                        .unwrap_or(0);
                                    pages::hint(
                                        format!(
                                            "{} · {}",
                                            scope["owner"].as_str().unwrap_or_default(),
                                            catalog.format(
                                                "machines.session-count",
                                                &[processes.to_string(), plugins.to_string()]
                                            )
                                        ),
                                        cx,
                                    )
                                }),
                        )
                        .when(
                            environment["sessions"].as_array().is_none_or(Vec::is_empty),
                            |el| el.child(pages::hint(catalog.text("machines.no-sessions"), cx)),
                        )
                        .child(pages::hint(
                            catalog.text(if machine["connection"] == "online" {
                                "machines.online"
                            } else {
                                "machines.offline-detail"
                            }),
                            cx,
                        )),
                );
            }
        } else {
            let capabilities = machine["capabilities"]
                .as_array()
                .map(|c| {
                    c.iter()
                        .filter_map(Value::as_str)
                        .filter_map(|key| match key {
                            "runtime" => Some(catalog.text("machines.runtime")),
                            "conversation" => Some(catalog.text("mobile.chat")),
                            "tasks" => Some(catalog.text("mobile.tasks")),
                            "host_execution" => Some("Host"),
                            "sandbox_execution" => Some("Sandbox"),
                            _ => None,
                        })
                        .collect::<Vec<_>>()
                        .join(", ")
                })
                .unwrap_or_default();
            detail = detail.child(
                div()
                    .p_8()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(pages::title(catalog.text("machines.details")))
                    .child(pages::hint(
                        machine["runtime_id"]
                            .as_str()
                            .unwrap_or_default()
                            .to_owned(),
                        cx,
                    ))
                    .child(pages::hint(
                        format!("{}: {capabilities}", catalog.text("machines.capabilities")),
                        cx,
                    ))
                    .child(pages::hint(
                        catalog.text(if machine["connection"] == "online" {
                            "machines.online"
                        } else {
                            "machines.offline-detail"
                        }),
                        cx,
                    ))
                    .when(
                        machine["environments"].as_array().is_none_or(Vec::is_empty),
                        |el| el.child(pages::hint(catalog.text("machines.empty-detail"), cx)),
                    ),
            );
        }
    } else {
        detail = detail.child(
            div()
                .p_8()
                .child(pages::hint(catalog.text("machines.loading"), cx)),
        );
    }
    pages::page(layout, cx)
        .flex()
        .when(layout.compact, |el| el.flex_col())
        .child(sidebar)
        .child(detail)
}
