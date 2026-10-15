use crate::markdown;
use crate::pages::Control;
use gpui::{prelude::*, *};
use gpui_kit::component::{
    IconName, Sizable,
    button::{Button, ButtonVariants},
    clipboard::Clipboard,
};
use pot_protocol::{Message, MessageStatus, Role};
use std::sync::Arc;

#[derive(Clone, Debug)]
pub enum ChatAction {
    ToggleTool(String),
    OpenTask(String),
    DownloadFile(String),
}
pub type ChatHandler = Arc<dyn Fn(&ChatAction, &mut Window, &mut App) + Send + Sync>;

pub struct Labels {
    pub tool: String,
    pub running: String,
    pub failed: String,
    pub stopped: String,
    pub done: String,
    pub unfinished: String,
    pub applied: String,
    pub received: String,
    pub copy: String,
    pub view_task: String,
}
impl Labels {
    pub fn new(mut translate: impl FnMut(&str) -> String) -> Self {
        Self {
            tool: translate("client.tool"),
            running: translate("schedules-ui.running"),
            failed: translate("schedules-ui.failed"),
            stopped: translate("main.stopped"),
            done: translate("main.done"),
            unfinished: translate("main.unfinished-see-the-reason-above"),
            applied: translate("main.applied-running"),
            received: translate("main.received-will-apply-next-turn"),
            copy: translate("main.copy-message"),
            view_task: translate("main.view-task"),
        }
    }
}
#[derive(Clone, Copy)]
pub struct MessageOptions {
    pub dark: bool,
    pub expanded: bool,
    pub latest_reply: bool,
    pub touch: bool,
    pub available_width: f32,
}
impl MessageOptions {
    fn message_width(self, user: bool) -> f32 {
        let desktop: f32 = if user { 620. } else { 720. };
        if self.touch {
            desktop.min(self.available_width.max(0.))
        } else {
            desktop
        }
    }
}
pub struct MessageView<'a> {
    pub message: &'a Message,
    pub source: &'a str,
    pub labels: &'a Labels,
    pub options: MessageOptions,
    pub artifact: Option<AnyElement>,
}
pub fn task_report_id(m: &Message) -> Option<String> {
    m.id.starts_with("task-report-")
        .then(|| {
            m.text
                .rsplit_once("任务编号：")
                .or_else(|| m.text.rsplit_once("Task ID:"))
                .map(|(_, id)| id.trim().to_owned())
        })
        .flatten()
        .filter(|id| !id.is_empty())
}
/// Only generated wrappers are translated; canonical user/model text is literal.
pub fn display_source(
    m: &Message,
    report: impl FnOnce(&str) -> String,
    notice: impl FnOnce(&str) -> String,
) -> String {
    if m.id.starts_with("task-report-") {
        report(
            m.text
                .rsplit_once("\n任务编号：")
                .or_else(|| m.text.rsplit_once("\nTask ID:"))
                .map(|(text, _)| text)
                .unwrap_or(&m.text),
        )
    } else if m.role == Role::System
        || m.request_id
            .as_deref()
            .is_some_and(|id| id.starts_with("pot-result:"))
    {
        notice(&m.text)
    } else {
        m.text.clone()
    }
}
pub fn message(view: MessageView<'_>, handler: ChatHandler, cx: &App) -> Div {
    let m = view.message;
    let options = view.options;
    let labels = view.labels;
    let source = view.source;
    let artifact = view.artifact;
    let dark = options.dark;
    let ink = if dark { 0xf2f2f2 } else { 0x262626 };
    let muted = if dark { 0xb7b7b7 } else { 0x646464 };
    let bubble = if dark { 0x2b2b2b } else { 0xffffff };
    let latest_reply = options.latest_reply;
    let user = m.role == Role::User;
    let tool = m.role == Role::Tool;
    let expanded = options.expanded;
    let id = m.id.clone();
    let label = if tool {
        format!(
            "{} {} · {}",
            if expanded { "⌄" } else { "›" },
            m.text.lines().next().unwrap_or(labels.tool.as_str()),
            match m.status {
                MessageStatus::Streaming | MessageStatus::Accepted => labels.running.clone(),
                MessageStatus::Failed => labels.failed.clone(),
                MessageStatus::Cancelled => labels.stopped.clone(),
                _ => labels.done.clone(),
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
                .max_w(px(options.message_width(user)))
                .min_w_0()
                .when(user, |el| {
                    el.px_4().py_3().rounded(px(18.)).bg(rgb(if dark {
                        0x243a52
                    } else {
                        0xe7f2ff
                    }))
                })
                .when(tool, |el| el.px_3().py_2().rounded(px(10.)).bg(rgb(bubble)))
                .text_color(rgb(if user {
                    if dark { 0xe3efff } else { 0x25435f }
                } else {
                    ink
                }))
                .when(tool, |el| {
                    el.child(
                        Button::new(SharedString::from(format!("tool-{}", m.id)))
                            .ghost()
                            .cursor_pointer()
                            .control(cx)
                            .text_size(px(if options.touch { 14. } else { 13. }))
                            .label(label)
                            .on_click({
                                let handler = handler.clone();
                                move |_, window, cx| {
                                    handler(&ChatAction::ToggleTool(id.clone()), window, cx)
                                }
                            }),
                    )
                })
                .when(!tool || expanded, |el| {
                    el.child(markdown::render(
                        &m.id,
                        source,
                        user,
                        dark,
                        Some(handler.clone()),
                        cx,
                    ))
                })
                .when_some(artifact, |el, extension| el.child(extension))
                .when(
                    matches!(m.status, MessageStatus::Failed | MessageStatus::Cancelled),
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
                                .child(if m.status == MessageStatus::Cancelled {
                                    labels.stopped.clone()
                                } else {
                                    labels.unfinished.clone()
                                }),
                        )
                    },
                )
                .when(user && m.status == MessageStatus::Streaming, |el| {
                    el.child(div().text_size(px(11.)).child(labels.applied.clone()))
                })
                .when(user && m.status == MessageStatus::Accepted, |el| {
                    el.child(div().text_size(px(11.)).child(labels.received.clone()))
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
                    .when(!latest_reply && !options.touch, |row| {
                        row.invisible()
                            .group_hover(SharedString::from(format!("message-{}", m.id)), |style| {
                                style.visible()
                            })
                    })
                    .when(!options.touch, |row| {
                        row.child(
                            Clipboard::new(SharedString::from(format!("copy-{}", m.id)))
                                .value(m.text.clone())
                                .tooltip(labels.copy.clone())
                                .small(),
                        )
                    })
                    .when(options.touch, |row| {
                        let text = m.text.clone();
                        row.child(
                            Button::new(SharedString::from(format!("copy-touch-{}", m.id)))
                                .ghost()
                                .control(cx)
                                .icon(IconName::Copy)
                                .tooltip(labels.copy.clone())
                                .accessibility_label(labels.copy.clone())
                                .on_click(move |_, _, cx| {
                                    cx.write_to_clipboard(ClipboardItem::new_string(text.clone()))
                                }),
                        )
                    })
                    .when_some(task_report_id(m), |row, task| {
                        row.child(
                            Button::new(SharedString::from(format!("open-{}", m.id)))
                                .ghost()
                                .control(cx)
                                .icon(IconName::ExternalLink)
                                .tooltip(labels.view_task.clone())
                                .accessibility_label(labels.view_task.clone())
                                .on_click({
                                    let handler = handler.clone();
                                    move |_, window, cx| {
                                        handler(&ChatAction::OpenTask(task.clone()), window, cx)
                                    }
                                }),
                        )
                    }),
            )
        })
}
