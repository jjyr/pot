extern crate gpui_kit as gpui;

mod artifacts;
mod client;
mod markdown;
mod schedules_ui;
#[cfg(test)]
mod stream;
mod ui;

use gpui::{div, prelude::*, px, rgb, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable, Theme, ThemeMode,
    button::{Button, ButtonVariants},
    clipboard::Clipboard,
    input::{Input, InputState},
    list::ListItem,
};
use pot_protocol::{MessageStatus, Role};
use std::{
    collections::HashSet,
    ffi::{CStr, CString},
};

unsafe extern "C" {
    fn pot_bridge_set_wake(callback: extern "C" fn());
    fn pot_bridge_install(html: *const std::ffi::c_char);
    fn pot_bridge_hide();
    fn pot_bridge_update(json: *const std::ffi::c_char);
    fn pot_bridge_event(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_ui(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_command(json: *const std::ffi::c_char);
    fn pot_bridge_dark() -> bool;
    fn pot_bridge_composer_height() -> f64;
}
static UI_WAKE: std::sync::OnceLock<async_channel::Sender<()>> = std::sync::OnceLock::new();
extern "C" fn wake_ui() {
    if let Some(sender) = UI_WAKE.get() {
        let _ = sender.try_send(());
    }
}
struct Shell {
    client: client::Client,
    scroll: ScrollHandle,
    last_content: String,
    expanded: HashSet<String>,
    first: bool,
    show_tasks: bool,
    last_view: Option<client::View>,
    last_dark: bool,
    last_height: f64,
    last_at_bottom: bool,
    jumping_to_latest: bool,
    native: serde_json::Value,
    settings: ui::Settings,
    task_search: Entity<InputState>,
    schedule_tab: bool,
    schedules_ui: schedules_ui::SchedulesUI,
}
impl Shell {
    fn tick(&mut self, cx: &mut Context<Self>) -> bool {
        let old_tasks = self.show_tasks;
        {
            let mut v = self.client.view.lock().unwrap();
            v.schedules_visible = self.show_tasks && self.schedule_tab;
        }
        let success = self.client.view.lock().unwrap().schedule_success;
        if success != self.schedules_ui.success {
            self.schedules_ui.success = success;
            self.schedules_ui.open = false;
        }
        let search = self.task_search.read(cx).value().to_string();
        let query = self.client.view.lock().unwrap().task_query.clone();
        if search != query.q {
            self.client.filter_tasks(query.scope, search, 0);
        }
        let mut buffer = vec![0i8; 524288];
        while unsafe { pot_bridge_event(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            let event: serde_json::Value =
                serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
                    .unwrap_or_default();
            match event["action"].as_str() {
                Some("send") => {
                    if let Some(text) = event["text"].as_str() {
                        self.client.send(text.into());
                    }
                }
                Some("copy-selection") => cx.dispatch_action(&gpui_kit::base::input::Copy),
                Some("select-all") => cx.dispatch_action(&gpui_kit::base::input::SelectAll),
                Some("paste") => cx.dispatch_action(&gpui_kit::base::input::Paste),
                Some("cut") => cx.dispatch_action(&gpui_kit::base::input::Cut),
                Some("undo") => cx.dispatch_action(&gpui_kit::base::input::Undo),
                Some("redo") => cx.dispatch_action(&gpui_kit::base::input::Redo),
                Some("cancel") => self.client.cancel(),
                Some("tasks") => {
                    self.show_tasks = true;
                }
                Some("main") => {
                    self.show_tasks = false;
                    self.client.select(String::new());
                    self.first = true;
                }
                _ => {}
            }
        }
        let native = if unsafe { pot_bridge_ui(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
                .unwrap_or_default()
        } else {
            serde_json::Value::Null
        };
        let native_changed = native != self.native;
        self.native = native;
        let accepted = self.client.view.lock().unwrap().accepted_text.take();
        let view = self.client.view.lock().unwrap().clone();
        let dark = unsafe { pot_bridge_dark() };
        let height = unsafe { pot_bridge_composer_height() };
        let mut changed = native_changed
            || self.last_view.as_ref() != Some(&view)
            || old_tasks != self.show_tasks
            || dark != self.last_dark
            || height != self.last_height;
        self.last_view = Some(view.clone());
        self.last_dark = dark;
        self.last_height = height;
        let snapshot = view.snapshot.as_ref();
        let running = snapshot.is_some_and(|s| s.turn.is_some());
        let messages = if view.selected == "new" {
            vec![]
        } else {
            snapshot.map(|s| s.messages.clone()).unwrap_or_default()
        };
        let at_bottom = self.scroll.max_offset().y + self.scroll.offset().y <= px(32.);
        // Scrolling changes the handle during layout, after render. Observe it
        // independently of Runtime snapshots so hover is not required to redraw.
        changed |= at_bottom != self.last_at_bottom;
        self.last_at_bottom = at_bottom;
        if at_bottom {
            self.jumping_to_latest = false;
        }

        // Include streaming text changes, not just durable event cursors.
        let content = messages
            .iter()
            .map(|m| format!("{}:{}:{:?}", m.id, m.text.len(), m.status))
            .collect::<Vec<_>>()
            .join(";");
        if content != self.last_content && (at_bottom || self.first || accepted.is_some()) {
            self.scroll.scroll_to_bottom();
        }
        if !messages.is_empty() {
            self.first = false;
        }
        self.last_content = content;
        let state = serde_json::json!({
            "boot_id":snapshot.map(|s|s.boot_id.as_str()),"has_snapshot":snapshot.is_some(),"running":running || view.main_snapshot.as_ref().is_some_and(|s|s.turn.is_some()) || view.task_page.counts.active > 0,"selected_running":running,"selected":view.selected,"task_list":self.show_tasks,"can_send":view.can_send(),"connected":view.error.is_empty() && snapshot.is_some(),
            "needs_auth":snapshot.is_some_and(|s|s.engine.phase == pot_protocol::EnginePhase::NeedsAuth),
            "error":snapshot.is_some_and(|s|s.engine.phase == pot_protocol::EnginePhase::Failed),
            "engine":snapshot.map(|s|s.engine.engine.as_str()).unwrap_or("正在连接"),
            "used":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.used),
            "limit":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.limit),
            "accepted":accepted,"at_bottom":at_bottom,
            "messages":view.main_snapshot.as_ref().map(|s|s.messages.as_slice()).unwrap_or(&[]).iter().map(|m|serde_json::json!({"id":m.id,"role":m.role,"status":m.status})).collect::<Vec<_>>()
        });
        if let Ok(json) = CString::new(state.to_string()) {
            unsafe { pot_bridge_update(json.as_ptr()) };
        }
        changed
    }
}
impl Render for Shell {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let view = self.client.view.lock().unwrap().clone();
        let snapshot = view.snapshot.as_ref();
        let running = snapshot.is_some_and(|s| s.turn.is_some());
        let stopping = snapshot
            .and_then(|s| s.turn.as_ref())
            .is_some_and(|t| t.cancelling);
        let messages = if view.selected == "new" {
            vec![]
        } else {
            snapshot.map(|s| s.messages.clone()).unwrap_or_default()
        };
        let at_bottom = self.scroll.max_offset().y + self.scroll.offset().y <= px(32.);
        let dark = unsafe { pot_bridge_dark() };
        let mode = if dark {
            ThemeMode::Dark
        } else {
            ThemeMode::Light
        };
        if Theme::global(cx).mode != mode {
            Theme::change(mode, Some(window), cx);
        }
        self.settings.sync(&self.native["settings"], window, cx);
        match self.native["page"].as_u64() {
            Some(2) => return self.settings.render(&self.native["settings"], cx),
            Some(1) => return ui::sandbox(&self.native, cx),
            _ => {}
        }
        let bg = if dark { 0x191a1d } else { 0xffffff };
        let ink = if dark { 0xededf0 } else { 0x24262b };
        let muted = if dark { 0xa2a5ae } else { 0x7e828b };
        let bubble = if dark { 0x292b30 } else { 0xf3f3f5 };
        let error = if !view.command_error.is_empty() {
            view.command_error.clone()
        } else {
            view.error.clone()
        };
        if self.show_tasks && self.schedule_tab {
            return self.schedules_ui.render(&view, cx);
        }
        if self.show_tasks {
            return ui::page(cx).child(
                ui::content()
                    .child(schedules_ui::navigation(false,cx))
                    .child(ui::title("任务"))
                    .child(ui::hint(
                        format!(
                            "向主 Agent 描述目标即可创建任务。最多同时处理 {} 个任务。",
                            self.native["settings"]["parallel"].as_str().unwrap_or("5")
                        ),
                        cx,
                    ))
                    .child(
                        div().flex().gap_2().children([
                            ("active", "进行中", view.task_page.counts.active),
                            ("attention", "需处理", view.task_page.counts.attention),
                            ("archived", "已归档", view.task_page.counts.archived),
                        ].into_iter().map(|(scope,label,count)| {
                            Button::new(SharedString::from(format!("task-scope-{scope}")))
                                .ghost().label(format!("{label} {count}"))
                                .when(view.task_query.scope==scope, |b|b.bg(cx.theme().secondary))
                                .on_click(cx.listener(move |this,_,_,cx| {
                                    let q=this.client.view.lock().unwrap().task_query.q.clone();
                                    this.client.filter_tasks(scope.into(),q,0);cx.notify();
                                }))
                        }))
                    )
                    .child(Input::new(&self.task_search).aria_label("搜索任务标题或编号"))
                    .child(
                        div()
                            .id(SharedString::from(format!("task-list-{}-{}-{}",view.task_query.scope,view.task_query.q,view.task_query.offset)))
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .when(view.tasks.is_empty(), |el| {
                                el.child(
                                    div().py_12().text_color(cx.theme().muted_foreground).child(
                                        if !view.error.is_empty() && view.main_snapshot.is_some() {
                                            "暂时无法读取任务，请检查 Sandbox 状态。"
                                        } else if view.tasks_loading || view.main_snapshot.is_none() {
                                            "正在连接 Sandbox，读取任务…"
                                        } else {
                                            if !view.task_query.q.is_empty() { "当前分组没有匹配任务。可切换分组查找历史任务。" }
                                            else { match view.task_query.scope.as_str() {
                                                "archived" => "还没有归档任务。完成或取消的任务会自动移到这里。",
                                                "attention" => "没有需要处理的任务。失败或中断的任务会显示在这里。",
                                                _ => "没有进行中的任务。直接聊天或新建一个任务。",
                                            }}
                                        },
                                    ),
                                )
                            })
                            .children(view.tasks.iter().map(|task| {
                                let id = task.id.clone();
                                let active = matches!(
                                    task.phase,
                                    pot_protocol::TaskPhase::Running
                                        | pot_protocol::TaskPhase::Queued
                                );
                                ListItem::new(SharedString::from(format!("task-{id}")))
                                    .accessibility_label(task.title.clone())
                                    .flex_col()
                                    .items_start()
                                    .gap_2()
                                    .p_5()
                                    .mb_3()
                                    .rounded_lg()
                                    .bg(cx.theme().background)
                                    .border_1()
                                    .border_color(cx.theme().border)
                                    .cursor_pointer()
                                    .child(
                                        div()
                                            .w_full()
                                            .flex()
                                            .items_center()
                                            .gap_3()
                                            .child(
                                                div()
                                                    .flex_1()
                                                    .min_w_0()
                                                    .truncate()
                                                    .text_size(px(15.))
                                                    .font_weight(FontWeight::MEDIUM)
                                                    .child(task.title.clone()),
                                            )
                                            .child(
                                                div()
                                                    .text_color(cx.theme().muted_foreground)
                                                    .child("›"),
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
                                                    .text_color(rgb(if active {
                                                        0x168bd2
                                                    } else if task.phase.scope()=="attention" {
                                                        0xb7791f
                                                    } else {
                                                        muted
                                                    }))
                                                    .child(task_phase(&task.phase)),
                                            )
                                            .when(task.pending > 0, |el| {
                                                el.child(
                                                    div()
                                                        .text_size(px(12.))
                                                        .text_color(cx.theme().muted_foreground)
                                                        .child(format!(
                                                            "· {} 条指导待处理",
                                                            task.pending
                                                        )),
                                                )
                                            }),
                                    )
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.show_tasks = false;
                                        this.client.select(id.clone());
                                        this.first = true;
                                        cx.notify();
                                    }))
                            })),
                    )
                    .child(div().flex().items_center().justify_between()
                        .child(ui::hint(if view.tasks_loading { "正在加载…".into() } else {
                            format!("共 {} 项 · 第 {} 页", view.task_page.total, view.task_query.offset / view.task_query.limit + 1)
                        },cx))
                        .child(div().flex().gap_2()
                            .child(Button::new("tasks-previous").ghost().label("上一页")
                                .disabled(view.tasks_loading || view.task_query.offset==0)
                                .on_click(cx.listener(|this,_,_,cx| {
                                    let q=this.client.view.lock().unwrap().task_query.clone();
                                    this.client.filter_tasks(q.scope,q.q,q.offset.saturating_sub(q.limit));cx.notify();
                                })))
                            .child(Button::new("tasks-next").ghost().label("下一页")
                                .disabled(view.tasks_loading || view.task_page.next_offset.is_none())
                                .on_click(cx.listener(|this,_,_,cx| {
                                    let v=this.client.view.lock().unwrap().clone();
                                    if let Some(offset)=v.task_page.next_offset {
                                        this.client.filter_tasks(v.task_query.scope,v.task_query.q,offset);cx.notify();
                                    }
                                })))
                        )
                    ),
            );
        }
        div()
            .size_full()
            .pl(px(56.))
            .flex()
            .flex_col()
            .bg(rgb(bg))
            .text_color(rgb(ink))
            .text_size(px(15.))
            .font_family(".AppleSystemUIFont")
            .when(!view.selected.is_empty(), |el| {
                let task = view
                    .selected_task
                    .as_ref()
                    .filter(|t| t.id == view.selected);
                el.child(
                    div()
                        .px_6()
                        .py_3()
                        .flex()
                        .gap_4()
                        .child(
                            Button::new("back-tasks")
                                .ghost()
                                .cursor_pointer()
                                .label("‹ 任务")
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.show_tasks = true;
                                    cx.notify();
                                })),
                        )
                        .child(
                            div().flex_1().child(
                                task.map(|t| {
                                    format!(
                                        "{} · {}{}",
                                        t.title,
                                        if t.phase.scope() == "archived" {
                                            "已归档 · "
                                        } else {
                                            ""
                                        },
                                        task_phase(&t.phase)
                                    )
                                })
                                .unwrap_or(
                                    if view.selected == "new" {
                                        "新任务 · 输入目标后发送"
                                    } else {
                                        "正在加载任务…"
                                    }
                                    .into(),
                                ),
                            ),
                        )
                        .when(
                            task.is_some_and(|t| {
                                matches!(
                                    t.phase,
                                    pot_protocol::TaskPhase::Running
                                        | pot_protocol::TaskPhase::Queued
                                )
                            }),
                            |el| {
                                el.child(
                                    Button::new("stop-task")
                                        .ghost()
                                        .cursor_pointer()
                                        .label("停止")
                                        .on_click(
                                            cx.listener(|this, _, _, _| this.client.cancel()),
                                        ),
                                )
                            },
                        ),
                )
            })
            .child(
                div()
                    .id("messages")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .track_scroll(&self.scroll)
                    .px_8()
                    .pt_8()
                    .pb_4()
                    .flex()
                    .flex_col()
                    .gap_5()
                    .when(messages.is_empty(), |el| {
                        el.child(
                            div()
                                .mx_auto()
                                .mt_16()
                                .text_color(rgb(muted))
                                .child("有什么想交给 Pot 的？"),
                        )
                    })
                    .children(messages.iter().enumerate().map(|(index, m)| {
                        let latest_reply =
                            !running && index + 1 == messages.len() && m.role == Role::Assistant;
                        let artifact = artifacts::from_message(
                            &m,
                            &if view.selected.is_empty() {
                                client::root().join("workspace/workspace")
                            } else {
                                client::root()
                                    .join("workspace/workspace/tasks")
                                    .join(&view.selected)
                            },
                        );
                        let user = m.role == Role::User;
                        let tool = m.role == Role::Tool;
                        let expanded = self.expanded.contains(&m.id);
                        let id = m.id.clone();
                        let label = if tool {
                            format!(
                                "{} {} · {}",
                                if expanded { "⌄" } else { "›" },
                                m.text.lines().next().unwrap_or("工具"),
                                match m.status {
                                    MessageStatus::Streaming | MessageStatus::Accepted => "执行中",
                                    MessageStatus::Failed => "失败",
                                    MessageStatus::Cancelled => "已停止",
                                    _ => "完成",
                                }
                            )
                        } else {
                            String::new()
                        };
                        div()
                            .w_full()
                            .max_w(px(760.))
                            .mx_auto()
                            .flex()
                            .flex_col()
                            .group(SharedString::from(format!("message-{}", m.id)))
                            .when(user, |el| el.items_end())
                            .when(!user, |el| el.items_start())
                            .child(
                                div()
                                    .id(SharedString::from(m.id.clone()))
                                    .relative()
                                    .max_w(px(if user { 620. } else { 720. }))
                                    .min_w_0()
                                    .px_4()
                                    .py_3()
                                    .rounded(px(18.))
                                    .bg(rgb(if user { 0x079fee } else { bubble }))
                                    .text_color(rgb(if user { 0xffffff } else { ink }))
                                    .when(tool, |el| {
                                        el.child(
                                            Button::new(SharedString::from(format!(
                                                "tool-{}",
                                                m.id
                                            )))
                                            .ghost()
                                            .cursor_pointer()
                                            .text_size(px(13.))
                                            .label(label)
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                if !this.expanded.remove(&id) {
                                                    this.expanded.insert(id.clone());
                                                }
                                                cx.notify();
                                            })),
                                        )
                                    })
                                    .when(!tool || expanded, |el| {
                                        el.child(markdown::render(
                                            &m.id,
                                            if m.id.starts_with("task-report-") {
                                                m.text
                                                    .rsplit_once("\n任务编号：")
                                                    .map(|(text, _)| text)
                                                    .unwrap_or(&m.text)
                                            } else {
                                                &m.text
                                            },
                                            user,
                                            dark,
                                            cx,
                                        ))
                                    })
                                    .when_some(artifact, |el, path| {
                                        let label = format!(
                                            "▤ {} · 在 Finder 中显示",
                                            path.file_name().unwrap_or_default().to_string_lossy()
                                        );
                                        el.child(
                                            div()
                                                .id("artifact")
                                                .mt_2()
                                                .text_size(px(12.))
                                                .cursor_pointer()
                                                .text_color(rgb(0x079fee))
                                                .child(label)
                                                .on_click(move |_, _, _| {
                                                    let _ = std::process::Command::new("open")
                                                        .arg("-R")
                                                        .arg(&path)
                                                        .spawn();
                                                }),
                                        )
                                    })
                                    .when(
                                        matches!(
                                            m.status,
                                            MessageStatus::Failed | MessageStatus::Cancelled
                                        ),
                                        |el| {
                                            el.child(
                                                div()
                                                    .mt_2()
                                                    .text_size(px(12.))
                                                    .text_color(rgb(if user {
                                                        0xffffff
                                                    } else if m.status == MessageStatus::Failed {
                                                        0xc96a40
                                                    } else {
                                                        muted
                                                    }))
                                                    .child(
                                                        if m.status == MessageStatus::Cancelled {
                                                            "已停止"
                                                        } else {
                                                            "未完成 · 查看上方原因"
                                                        },
                                                    ),
                                            )
                                        },
                                    )
                                    .when(user && m.status == MessageStatus::Streaming, |el| {
                                        el.child(div().text_size(px(11.)).child("已应用 · 执行中"))
                                    })
                                    .when(user && m.status == MessageStatus::Accepted, |el| {
                                        el.child(
                                            div()
                                                .text_size(px(11.))
                                                .child("已接收 · 等待下一轮应用"),
                                        )
                                    }),
                            )
                            .when(!tool, |el| {
                                el.child(
                                    div()
                                        .flex()
                                        .items_center()
                                        .gap_1()
                                        .mt_1()
                                        .text_color(rgb(muted))
                                        // Reserve the row so hovering never moves messages.
                                        .when(!latest_reply, |row| {
                                            row.invisible().group_hover(
                                                SharedString::from(format!("message-{}", m.id)),
                                                |style| style.visible(),
                                            )
                                        })
                                        .child(
                                            Clipboard::new(SharedString::from(format!(
                                                "copy-{}",
                                                m.id
                                            )))
                                            .value(m.text.clone())
                                            .tooltip("复制消息")
                                            .small(),
                                        )
                                        .when_some(
                                            m.id.starts_with("task-report-")
                                                .then(|| {
                                                    m.text
                                                        .rsplit_once("任务编号：")
                                                        .map(|(_, id)| id.trim().to_string())
                                                })
                                                .flatten()
                                                .filter(|id| !id.is_empty()),
                                            |row, task| {
                                                row.child(
                                                    Button::new(SharedString::from(format!(
                                                        "open-{}",
                                                        m.id
                                                    )))
                                                    .ghost()
                                                    .small()
                                                    .icon(IconName::ExternalLink)
                                                    .tooltip("查看任务")
                                                    .accessibility_label("查看任务")
                                                    .on_click(cx.listener(move |this, _, _, cx| {
                                                        this.client.select(task.clone());
                                                        this.show_tasks = false;
                                                        this.first = true;
                                                        cx.notify();
                                                    })),
                                                )
                                            },
                                        ),
                                )
                            })
                    })),
            )
            .child(
                div().w_full().px_8().flex_shrink_0().child(
                    div()
                        .w_full()
                        .max_w(px(760.))
                        .mx_auto()
                        .text_size(px(12.))
                        .text_color(rgb(muted))
                        .when(!error.is_empty(), |el| el.child(div().py_2().child(error)))
                        .when(running, |el| {
                            el.child(div().py_2().child(if stopping {
                                "正在停止…"
                            } else {
                                "Pot 正在处理…"
                            }))
                        })
                        .when(
                            snapshot
                                .and_then(|s| s.budget.as_ref())
                                .is_some_and(|b| b.remaining() == 0),
                            |el| el.child("请求额度已用完，可在设置 → Agent 中调整。"),
                        )
                        .when(!at_bottom && !self.jumping_to_latest, |el| {
                            el.child(
                                Button::new("latest")
                                    .ghost()
                                    .w_full()
                                    .h(px(32.))
                                    .bg(rgb(bubble))
                                    .cursor_pointer()
                                    .label("↓ 最新消息")
                                    .accessibility_label("滚动到最新消息")
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.jumping_to_latest = true;
                                        this.scroll.scroll_to_bottom();
                                        cx.notify();
                                    })),
                            )
                        }),
                ),
            )
            .child(
                div()
                    .flex_shrink_0()
                    // Native composer: 24 px bottom inset. Remove its extra
                    // 24 px top clearance while the latest-message bar is shown.
                    .h(px(unsafe { pot_bridge_composer_height() } as f32
                        - if !at_bottom && !self.jumping_to_latest {
                            24.
                        } else {
                            0.
                        })),
            )
    }
}
fn task_phase(p: &pot_protocol::TaskPhase) -> &'static str {
    use pot_protocol::TaskPhase::*;
    match p {
        Queued => "排队中",
        Running => "执行中",
        Complete => "已完成",
        Failed => "失败 · 可追加指导",
        Cancelled => "已停止",
        Interrupted => "已中断 · 追加指导后继续",
    }
}
fn main() {
    gpui_kit::application()
        .with_assets(gpui_kit::assets::Assets)
        .run(|cx: &mut App| {
            gpui_kit::init(cx);
            gpui_kit::open_window(
                WindowOptions {
                    window_bounds: Some(WindowBounds::Windowed(Bounds::centered(
                        None,
                        size(px(1080.), px(760.)),
                        cx,
                    ))),
                    window_min_size: Some(size(px(800.), px(700.))),
                    ..Default::default()
                },
                cx,
                |window, cx| {
                    window.set_window_title("Pot");
                    window.on_window_should_close(cx, |_, _| {
                        unsafe { pot_bridge_hide() };
                        false
                    });
                    cx.new(|cx: &mut Context<Shell>| {
                        let (sender, receiver) = async_channel::bounded(1);
                        let _ = UI_WAKE.set(sender.clone());
                        unsafe {
                            pot_bridge_set_wake(wake_ui);
                        }
                        // Runtime snapshots and native geometry still use a bounded poll;
                        // native commands bypass it and wake this task immediately.
                        cx.background_spawn(async move {
                            loop {
                                smol::Timer::after(std::time::Duration::from_millis(200)).await;
                                if sender.is_closed() {
                                    break;
                                }
                                let _ = sender.try_send(());
                            }
                        })
                        .detach();
                        cx.spawn(async move |this, cx| {
                            while receiver.recv().await.is_ok() {
                                if this
                                    .update(cx, |this, cx| {
                                        if this.tick(cx) {
                                            cx.notify();
                                        }
                                    })
                                    .is_err()
                                {
                                    break;
                                }
                            }
                        })
                        .detach();
                        Shell {
                            client: client::Client::start(),
                            scroll: ScrollHandle::new(),
                            last_content: String::new(),
                            expanded: HashSet::new(),
                            first: true,
                            show_tasks: false,
                            last_view: None,
                            last_dark: false,
                            last_height: 0.,
                            last_at_bottom: true,
                            jumping_to_latest: false,
                            native: serde_json::Value::Null,
                            settings: ui::Settings::new(window, cx),
                            schedule_tab: false,
                            schedules_ui: schedules_ui::SchedulesUI::new(window, cx),
                            task_search: cx.new(|cx| {
                                InputState::new(window, cx).placeholder("搜索任务标题或编号…")
                            }),
                        }
                    })
                },
            )
            .unwrap();
            let html = CString::new("<html></html>").unwrap();
            unsafe { pot_bridge_install(html.as_ptr()) };
            cx.activate(true);
        });
}
