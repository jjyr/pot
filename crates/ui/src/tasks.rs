use crate::pages::tr;
use crate::{
    design,
    localization::Catalog,
    pages::{self, Handler, Layout},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
    input::{Input, InputState},
    list::ListItem,
};
use serde_json::{Value, json};
pub struct TasksView<'a> {
    pub search: &'a Entity<InputState>,
    pub scroll: &'a ScrollHandle,
    pub navigation: Div,
    pub tasks: &'a [pot_protocol::TaskInfo],
    pub task_page: &'a pot_protocol::TaskPage,
    pub task_query: &'a pot_protocol::TaskQuery,
    pub tasks_loading: bool,
    pub connected: bool,
    pub error: &'a str,
    pub parallel: Option<&'a str>,
}
pub fn render(
    view: TasksView<'_>,
    layout: Layout,
    catalog: Catalog,
    handler: Handler,
    window: &mut Window,
    cx: &mut App,
) -> Div {
    let search = view.search;
    let scroll = view.scroll;
    pages::page(layout, cx).child(
        pages::content(layout)
            .child(view.navigation)
            .when_some(view.parallel, |body, parallel| {
                body.child(pages::hint(
                    tr!(catalog, "main.describe-a-goal-to-the-main-agent", parallel),
                    cx,
                ))
            })
            .child(
                pages::segments(cx).children(
                    [
                        (
                            "active",
                            catalog.text("main.ongoing"),
                            view.task_page.counts.active,
                        ),
                        (
                            "attention",
                            catalog.text("main.needs-attention"),
                            view.task_page.counts.attention,
                        ),
                        (
                            "archived",
                            catalog.text("main.archived"),
                            view.task_page.counts.archived,
                        ),
                    ]
                    .into_iter()
                    .map(|(scope, label, count)| {
                        Button::new(SharedString::from(format!("task-scope-{scope}")))
                            .ghost()
                            .when(!layout.native, |b| b.min_h(px(44.)))
                            .small()
                            .toggled(view.task_query.scope == scope)
                            .label(format!("{label} {count}"))
                            .when(view.task_query.scope == scope, |b| {
                                b.bg(cx.theme().secondary)
                            })
                            .on_click(pages::on(&handler, "task-filter", json!(scope)))
                    }),
                ),
            )
            .child(
                Input::new(search)
                    .small()
                    .when(!layout.native, |input| input.h(px(44.)))
                    .aria_label(catalog.text("main.search-task-title-or-id")),
            )
            .when(!view.error.is_empty() && view.connected, |el| {
                el.child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(pages::notice(
                            catalog.text("main.unable-to-refresh-tasks-showing-the-last"),
                            true,
                            cx,
                        ))
                        .child(pages::retry(&handler, catalog)),
                )
            })
            .child(
                div()
                    .id(SharedString::from(format!(
                        "task-list-{}-{}-{}",
                        view.task_query.scope, view.task_query.q, view.task_query.offset
                    )))
                    .track_scroll(scroll)
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .rounded(px(10.))
                    .when(!view.tasks.is_empty(), |el| {
                        el.border_1()
                            .border_color(cx.theme().border)
                            .bg(design::surface(cx))
                    })
                    .when(view.tasks.is_empty(), |el| {
                        el.child(pages::empty(
                            IconName::Inbox,
                            if !view.error.is_empty() && view.connected {
                                catalog.text("main.unable-to-load-tasks-check-the-local")
                            } else if view.tasks_loading || !view.connected {
                                catalog.text("main.connecting-to-local-backend-and-loading-tasks")
                            } else if !view.task_query.q.is_empty() {
                                catalog.text("main.no-matching-tasks-in-this-group-switch")
                            } else {
                                match view.task_query.scope.as_str() {
                                    "archived" => catalog
                                        .text("main.no-archived-tasks-yet-completed-or-cancelled"),
                                    "attention" => catalog
                                        .text("main.no-tasks-need-attention-failed-or-interrupted"),
                                    _ => catalog.text("main.no-ongoing-tasks-describe-a-goal-in"),
                                }
                            },
                            cx,
                        ))
                        .when(!view.task_query.q.is_empty(), |el| {
                            el.child(
                                Button::new("clear-task-search")
                                    .ghost()
                                    .when(!layout.native, |b| b.min_h(px(44.)))
                                    .small()
                                    .label(catalog.text("main.clear-search"))
                                    .on_click(pages::on(
                                        &handler,
                                        "task-clear-search",
                                        Value::Null,
                                    )),
                            )
                        })
                    })
                    .children(view.tasks.iter().map(|task| {
                        let id = task.id.clone();
                        let active = matches!(
                            task.phase,
                            pot_protocol::TaskPhase::Running
                                | pot_protocol::TaskPhase::Queued
                                | pot_protocol::TaskPhase::Waiting
                        );
                        ListItem::new(SharedString::from(format!("task-{id}")))
                            .accessibility_label(task.title.clone())
                            .flex_col()
                            .items_start()
                            .gap_2()
                            .p_4()
                            .h(px(80.))
                            .when(!layout.native, |el| el.h_auto().min_h(px(80.)))
                            .rounded(px(0.))
                            .bg(design::surface(cx))
                            .border_b_1()
                            .border_color(cx.theme().border)
                            .cursor_pointer()
                            .child(
                                div()
                                    .w_full()
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .child(pages::brand_icon(&task.executor_icon).size(px(16.)))
                                    .child(
                                        div()
                                            .id(SharedString::from(format!("task-title-{id}")))
                                            .tooltip({
                                                let title = task.title.clone();
                                                move |window, cx| {
                                                    pages::title_tooltip(title.clone(), window, cx)
                                                }
                                            })
                                            .flex_1()
                                            .min_w_0()
                                            .whitespace_nowrap()
                                            .overflow_hidden()
                                            .text_size(px(15.))
                                            .font_weight(FontWeight::MEDIUM)
                                            .child(pages::single_line_title(
                                                &task.title,
                                                15.,
                                                (window.viewport_size().width
                                                    - px(if layout.native {
                                                        pages::SIDEBAR
                                                    } else {
                                                        0.
                                                    }))
                                                .min(px(pages::PAGE_WIDTH))
                                                    - px(130.),
                                                layout.native,
                                                window,
                                                cx,
                                            )),
                                    )
                                    .child(
                                        div().text_color(cx.theme().muted_foreground).child("›"),
                                    ),
                            )
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_size(px(12.))
                                            .text_color(if active {
                                                cx.theme().primary
                                            } else if task.phase.scope() == "attention" {
                                                design::attention(cx)
                                            } else {
                                                cx.theme().muted_foreground
                                            })
                                            .child(phase(&task.phase, catalog))
                                            .child(format!(" · Sandbox: {}", task.sandbox_id)),
                                    )
                                    .when(task.pending > 0, |el| {
                                        el.child(
                                            div()
                                                .text_size(px(12.))
                                                .text_color(cx.theme().muted_foreground)
                                                .child(tr!(
                                                    catalog,
                                                    "main.pending-instructions",
                                                    task.pending
                                                )),
                                        )
                                    }),
                            )
                            .on_click(pages::on(&handler, "task-open", json!(id)))
                    })),
            )
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(pages::hint(
                        if view.tasks_loading {
                            catalog.text("client.loading").into()
                        } else {
                            tr!(
                                catalog,
                                "main.items-page",
                                view.task_page.total,
                                view.task_query.offset / view.task_query.limit + 1
                            )
                        },
                        cx,
                    ))
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("tasks-previous")
                                    .ghost()
                                    .when(!layout.native, |b| b.min_h(px(44.)))
                                    .label(catalog.text("schedules-ui.previous"))
                                    .disabled(view.tasks_loading || view.task_query.offset == 0)
                                    .on_click(pages::on(&handler, "task-previous", Value::Null)),
                            )
                            .child(
                                Button::new("tasks-next")
                                    .ghost()
                                    .when(!layout.native, |b| b.min_h(px(44.)))
                                    .label(catalog.text("schedules-ui.next-52"))
                                    .disabled(
                                        view.tasks_loading || view.task_page.next_offset.is_none(),
                                    )
                                    .on_click(pages::on(&handler, "task-next", Value::Null)),
                            ),
                    ),
            ),
    )
}
pub fn phase(p: &pot_protocol::TaskPhase, catalog: Catalog) -> &'static str {
    use pot_protocol::TaskPhase::*;
    match p {
        Queued => catalog.text("schedules-ui.queued"),
        Waiting => catalog.text("main.waiting-for-allowance"),
        Running => catalog.text("schedules-ui.running"),
        Complete => catalog.text("schedules-ui.completed"),
        Failed => catalog.text("main.failed-add-instructions-to-continue"),
        Cancelled => catalog.text("main.stopped"),
        Interrupted => catalog.text("main.interrupted-add-instructions-to-resume"),
    }
}
