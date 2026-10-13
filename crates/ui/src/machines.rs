//! Machine status and owner-controlled access, limits and sandbox assignments.
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
    let mut body = div().p_4().flex().flex_col().gap_4();
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
        for capability in ["sandbox", "host"]
            .into_iter()
            .filter(|_| !view.permissions.is_null())
        {
            let current = view.permissions["policy"]["rules"]
                .as_array()
                .into_iter()
                .flatten()
                .find(|r| {
                    r["assistant_id"] == view.permissions["assistant_id"]
                        && r["scope"]["machine_id"] == runtime
                        && r["scope"]["capability"] == capability
                })
                .and_then(|r| r["decision"].as_str())
                .unwrap_or("ask");
            let mut controls = div().flex().flex_wrap().gap_2().child(format!(
                "{} · {}",
                view.permissions["assistant_id"].as_str().unwrap_or(""),
                capability
            ));
            for decision in ["ask", "allow", "deny"] {
                controls=controls.child(Button::new(SharedString::from(format!("policy-{runtime}-{capability}-{decision}")))
                    .ghost().label(catalog.text(&format!("permissions.policy-{decision}"))).disabled(current==decision)
                    .on_click(pages::on(&handler,"permission-decision",json!({"action":"configure","machine_id":runtime,"capability":capability,"decision":decision}))));
            }
            card = card.child(controls);
        }
        for grant in view.permissions["grants"]
            .as_array()
            .into_iter()
            .flatten()
            .filter(|g| g["scope"]["machine_id"] == runtime)
        {
            card = card.child(
                Button::new(SharedString::from(format!("revoke-{}", grant["id"])))
                    .ghost()
                    .label(format!(
                        "{} · {}",
                        catalog.text("permissions.revoke"),
                        grant["scope"]["capability"].as_str().unwrap_or("")
                    ))
                    .on_click(pages::on(
                        &handler,
                        "permission-decision",
                        json!({"id":grant["id"],"action":"revoke"}),
                    )),
            );
        }
        let limits = view.permissions["policy"]["sandbox_limits"]
            .get(runtime)
            .cloned()
            .unwrap_or(json!({"max_sandboxes":16,"cpus":4,"memory_gib":8,"disk_gib":32}));
        for key in ["max_sandboxes", "cpus", "memory_gib", "disk_gib"]
            .into_iter()
            .filter(|_| !view.permissions.is_null())
        {
            let n = limits[key].as_u64().unwrap_or(1);
            let mut row = div().flex().items_center().gap_2().child(format!(
                "{}: {n}",
                catalog.text(&format!("permissions.limit-{key}"))
            ));
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
                    .ghost()
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
            if !host && !view.permissions.is_null() {
                let sandbox = reference["target"]["sandbox_id"].as_str().unwrap_or("");
                let access = view.permissions["policy"]["sandboxes"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .find(|r| r["machine_id"] == runtime && r["sandbox_id"] == sandbox);
                let assistant = view.permissions["assistant_id"].as_str().unwrap_or("");
                row=row.child(Button::new(SharedString::from(format!("sandbox-owner-{runtime}-{sandbox}"))).ghost()
                    .label(catalog.text("permissions.assign-sandbox"))
                    .disabled(access.is_some_and(|r|r["owner"]==assistant))
                    .on_click(pages::on(&handler,"permission-decision",json!({"action":"configure","machine_id":runtime,"sandbox_access":{"machine_id":runtime,"sandbox_id":sandbox,"owner":assistant,"shared_with":[]}}))));
            }
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
