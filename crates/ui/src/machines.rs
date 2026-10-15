//! Machine-wide execution policy and shared development environment.
use crate::pages::Control;
use crate::{
    localization::Catalog,
    pages::{self, Handler, Layout},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};

pub struct MachinesView<'a> {
    pub directory: &'a Value,
    pub permissions: &'a Value,
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
    pages::page(layout, cx).flex().flex_col().child(
        div()
            .id("machine-status-list")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .child(content(view, catalog, handler, cx)),
    )
}
pub fn content(view: MachinesView<'_>, catalog: Catalog, handler: Handler, cx: &mut App) -> Div {
    let machines = view.directory["machines"]
        .as_array()
        .cloned()
        .unwrap_or_default();
    let selected = crate::machine_state::selection(view.directory, view.preferred);
    let mut body = div().flex().flex_col().gap_4();
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
        let settings_open = view.preferred["settings_machine"].as_str() == Some(runtime);
        let mut card = pages::card(cx).child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(gpui_kit::component::Icon::new(
                    gpui_kit::assets::IconName::Monitor,
                ))
                .child(
                    div()
                        .flex_1()
                        .font_weight(FontWeight::MEDIUM)
                        .child(name.to_owned()),
                )
                .child(
                    pages::hint(
                        catalog.text(if online {
                            "machines.online"
                        } else {
                            "machines.offline"
                        }),
                        cx,
                    )
                    .text_color(if online {
                        cx.theme().success
                    } else {
                        cx.theme().muted_foreground
                    }),
                )
                .when(!view.permissions.is_null(), |el| {
                    el.child(pages::action_tooltip(
                        SharedString::from(format!("machine-settings-tip-{runtime}")),
                        catalog.text("ui.settings"),
                        Button::new(SharedString::from(format!("machine-settings-{runtime}")))
                            .ghost()
                            .control(cx)
                            .icon(IconName::Settings)
                            .toggled(settings_open)
                            .accessibility_label(catalog.text("ui.settings"))
                            .on_click(pages::on(
                                &handler,
                                "machine-select",
                                if settings_open {
                                    Value::Null
                                } else {
                                    json!({"settings_machine":runtime})
                                },
                            )),
                    ))
                }),
        );
        card = card.child(pages::hint(
            machine["tags"]
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(Value::as_str)
                .collect::<Vec<_>>()
                .join(" · "),
            cx,
        ));
        if settings_open {
            card = card.child(
                Button::new(SharedString::from(format!("edit-tags-{runtime}")))
                    .ghost()
                    .control(cx)
                    .label(catalog.text("machines.edit-tags"))
                    .on_click(pages::on(&handler, "machine-labels", json!(runtime))),
            );
        }
        let policy = view.permissions["policy"]["machines"]
            .get(runtime)
            .cloned()
            .unwrap_or(json!({"execution_mode":"sandbox","host_access":"ask"}));
        if settings_open && !view.permissions.is_null() {
            card = card.child(pages::title(catalog.text("machines.execution-mode")));
            for mode in ["sandbox", "host"] {
                let mut next = policy.clone();
                next["execution_mode"] = json!(mode);
                card = card.child(
                    Button::new(SharedString::from(format!(
                        "execution-mode-{runtime}-{mode}"
                    )))
                    .ghost()
                    .control(cx)
                    .label(catalog.text(&format!("machines.execution-{mode}")))
                    .toggled(policy["execution_mode"] == mode)
                    .disabled(mode == "host" && policy["host_access"] == "deny")
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"action":"configure","machine_id":runtime,"machine_policy":next}),
                    )),
                );
            }
            card = card.child(pages::hint(
                catalog.text(if policy["execution_mode"] == "host" {
                    "machines.execution-host-detail"
                } else {
                    "machines.execution-sandbox-detail"
                }),
                cx,
            ));
            card = card.child(pages::title(catalog.text("machines.host-access")));
            let mut controls = div().flex().flex_wrap().gap_2();
            for decision in ["ask", "allow", "deny"] {
                let mut next = policy.clone();
                next["host_access"] = json!(decision);
                controls = controls.child(
                    Button::new(SharedString::from(format!(
                        "host-access-{runtime}-{decision}"
                    )))
                    .ghost()
                    .control(cx)
                    .label(catalog.text(&format!("machines.host-{decision}")))
                    .toggled(policy["host_access"] == decision)
                    .disabled(decision == "deny" && policy["execution_mode"] == "host")
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"action":"configure","machine_id":runtime,"machine_policy":next}),
                    )),
                );
            }
            card = card.child(controls);
        }
        let shared = machine["execution_targets"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|e| e["shared"] == true);
        let shared_running = shared.is_some_and(|e| e["state"]["phase"] == "running");
        let shared_busy = shared.is_some_and(|e| {
            e["active_leases"].as_u64().unwrap_or(0) > 0
                || e["unsettled_operations"].as_u64().unwrap_or(0) > 0
        });
        if settings_open && policy["execution_mode"] == "sandbox" {
            let action = if shared_running { "stop" } else { "start" };
            card = card
                .child(
                    Button::new(SharedString::from(format!("shared-lifecycle-{runtime}")))
                        .ghost()
                        .control(cx)
                        .label(catalog.text(if shared_running {
                            "machines.stop-shared"
                        } else {
                            "machines.start-shared"
                        }))
                        .disabled(!online || shared_busy)
                        .on_click(pages::on(
                            &handler,
                            "sandbox-lifecycle",
                            json!({"runtime_id":runtime,"sandbox_id":"shared","action":action}),
                        )),
                )
                .child(pages::hint(
                    catalog.text("machines.shared-resources-detail"),
                    cx,
                ));
        }
        let limits = shared
            .map(|e| e["config"].clone())
            .unwrap_or(json!({"cpus":4,"memory_gib":8,"disk_gib":32}));
        for key in ["cpus", "memory_gib", "disk_gib"]
            .into_iter()
            .filter(|key| {
                settings_open
                    && !view.permissions.is_null()
                    && policy["execution_mode"] == "sandbox"
                    && (*key != "disk_gib"
                        || shared.is_none_or(|e| e["config"]["disk_gib"].as_u64().unwrap_or(0) > 0))
            })
        {
            let n = limits[key].as_u64().unwrap_or(1);
            let mut row = div()
                .flex()
                .items_center()
                .gap_2()
                .child(div().flex_1().child(format!(
                    "{}: {n}",
                    catalog.text(&format!("permissions.limit-{key}"))
                )));
            for increase in [false, true] {
                let mut next = limits.clone();
                next[key] = json!(if increase {
                    n.saturating_add(1)
                } else {
                    n.saturating_sub(1).max(1)
                });
                row = row.child(
                    Button::new(SharedString::from(format!(
                        "limit-{runtime}-{key}-{increase}"
                    )))
                    .control(cx)
                    .w(px(28.))
                    .disabled(
                        shared_running
                            || shared_busy
                            || (!increase && n <= if key == "disk_gib" { 4 } else { 1 }),
                    )
                    .label(if increase { "+" } else { "−" })
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"action":"configure","machine_id":runtime,"sandbox_limits":next}),
                    )),
                );
            }
            card = card.child(row);
        }
        let execution_targets = machine["execution_targets"]
            .as_array()
            .cloned()
            .unwrap_or_default();
        if execution_targets.is_empty() {
            card = card.child(pages::hint(catalog.text("machines.empty"), cx));
        }
        for execution_target in execution_targets {
            let reference = &execution_target["execution_target"];
            let host = reference["target"]["kind"] == "host";
            let title = if host {
                "Host"
            } else {
                execution_target["name"].as_str().unwrap_or("Sandbox")
            };
            let expanded = view.preferred.get("target").is_some() && selected == *reference;
            let phase = execution_target["state"]["phase"]
                .as_str()
                .unwrap_or("unknown");
            let status = if !online {
                catalog.text("machines.offline")
            } else if host {
                catalog.text("machines.online")
            } else {
                catalog.phase(phase)
            };
            let mut row = div().flex().flex_col().gap_2().child(
                Button::new(SharedString::from(format!(
                    "execution-target-{runtime}-{}",
                    reference["target"]
                )))
                .ghost()
                .w_full()
                .h(px(36.))
                .when(crate::design::mobile(cx), |b| b.control(cx))
                .justify_start()
                .icon(if expanded {
                    IconName::ChevronDown
                } else {
                    IconName::ChevronRight
                })
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
                    &execution_target,
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
                                        "execution-target-tasks-{runtime}-{}",
                                        reference["target"]
                                    )))
                                    .ghost()
                                    .label(catalog.text("machines.view-tasks"))
                                    .on_click(pages::on(
                                        &handler,
                                        "execution-target-tasks",
                                        reference.clone(),
                                    )),
                                )
                                .when(desktop, |el| {
                                    el.child(
                                        Button::new(SharedString::from(format!(
                                            "execution-target-desktop-{runtime}-{}",
                                            reference["target"]
                                        )))
                                        .ghost()
                                        .label(catalog.text("machines.open-desktop"))
                                        .disabled(!online || phase != "running")
                                        .on_click(
                                            pages::on(
                                                &handler,
                                                "execution-target-desktop",
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
    body
}
