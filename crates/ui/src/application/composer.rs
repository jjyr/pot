//! Shared composer geometry and editor lifecycle. Native text input is supplied by AppKit.
use super::{Shell, client::View, i18n, platform, ui};
use crate::{
    design,
    state::{Drafts, Receipt},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, Sizable,
    button::{Button, ButtonVariants},
    input::{Textarea, TextareaState},
};
use serde_json::json;
pub struct Composer {
    pub height: f32,
    completion_context: Option<(String, usize)>,
    input: Entity<TextareaState>,
    watch: Subscription,
    drafts: Drafts<Entity<TextareaState>>,
    owner: String,
    receipts: Vec<Receipt>,
    native_drafts: Drafts<String>,
    native_owner: String,
    completion_index: usize,
    dismissed: Option<(String, usize)>,
    keys: Vec<String>,
}
impl Composer {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let input = cx.new(|cx| TextareaState::new(window, cx));
        let watch = cx.observe(&input, |_, _, cx| cx.notify());
        Self {
            height: 48.,
            completion_context: None,
            input,
            watch,
            drafts: Default::default(),
            owner: String::new(),
            receipts: vec![],
            native_drafts: Default::default(),
            native_owner: String::new(),
            completion_index: 0,
            dismissed: None,
            keys: vec![],
        }
    }
    pub fn sync(&mut self, view: &View, window: &mut Window, cx: &mut Context<Shell>) {
        let owner = format!(
            "{}/{}/{}",
            view.client_mode,
            view.runtime["runtime_id"].as_str().unwrap_or_default(),
            if view.selected.is_empty() {
                view.assistant_id()
            } else {
                &view.selected
            }
        );
        if self.owner != owner {
            self.drafts.save(&self.owner, self.input.clone());
            self.input = self
                .drafts
                .get(&owner)
                .unwrap_or_else(|| cx.new(|cx| TextareaState::new(window, cx)));
            self.watch = cx.observe(&self.input, |_, _, cx| cx.notify());
            self.owner = owner;
        }
        for receipt in self.receipts.drain(..) {
            let key = format!(
                "{}/{}/{}",
                view.client_mode,
                view.runtime["runtime_id"].as_str().unwrap_or_default(),
                receipt.conversation
            );
            let editor = if key == self.owner {
                Some(self.input.clone())
            } else {
                self.drafts.get(&key)
            };
            if let Some(editor) = editor
                && receipt.clears(&receipt.conversation, editor.read(cx).value().as_str())
            {
                editor.update(cx, |input, cx| input.set_value("", window, cx));
            }
        }
    }
    pub fn accept(&mut self, text: String, conversation: String) {
        self.receipts.push(Receipt {
            id: String::new(),
            text,
            conversation,
        });
    }
    pub fn key(&mut self, key: String) {
        self.keys.push(key);
    }
    pub fn discard_keys(&mut self) {
        self.keys.clear();
    }
    pub fn native_sync(&mut self, view: &View) {
        let Some(editor) = platform::with(|p| p.editor()) else {
            return;
        };
        let conversation = if view.selected.is_empty() {
            view.assistant_id()
        } else {
            &view.selected
        };
        let owner = format!(
            "{}/{}/{}",
            view.client_mode,
            view.runtime["runtime_id"].as_str().unwrap_or_default(),
            conversation
        );
        let mut text = editor.text;
        if self.native_owner != owner {
            self.keys.clear();
            self.native_drafts.save(&self.native_owner, text);
            text = self.native_drafts.get(&owner).unwrap_or_default();
            self.native_owner = owner;
            ui::command(json!({"action":"editor-set","text":text}));
        }
        for receipt in self.receipts.drain(..) {
            if receipt.clears(conversation, &text) {
                ui::command(json!({"action":"editor-set","text":""}));
            } else {
                let key = format!(
                    "{}/{}/{}",
                    view.client_mode,
                    view.runtime["runtime_id"].as_str().unwrap_or_default(),
                    receipt.conversation
                );
                if self
                    .native_drafts
                    .get(&key)
                    .is_some_and(|draft| draft == receipt.text)
                {
                    self.native_drafts.save(&key, String::new());
                }
            }
        }
    }
}
impl Shell {
    pub(super) fn composer_frame(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let view = self.client.view.lock().unwrap().clone();
        let native = platform::with(|p| p.capabilities().external_text_input);
        let height = self.composer.height;
        let touch = design::touch(cx);
        let width = (f32::from(window.fully_visible_bounds().size.width)
            - (design::sidebar_width(cx) + if design::mobile(cx) { 32. } else { 64. })
            - self.assistant_pane_width(cx))
        .clamp(0., 760.);
        let retry = view.pending
            && !view.command_error.is_empty()
            && platform::with(|p| p.capabilities().explicit_send_retry);
        let stop = view.snapshot.as_ref().is_some_and(|s| s.turn.is_some())
            && self.editor_snapshot(cx).text.trim().is_empty();
        let running = view.snapshot.as_ref().is_some_and(|s| s.turn.is_some());
        let editor = self.editor_snapshot(cx);
        let context = (editor.text, editor.caret);
        if self.composer.completion_context.as_ref() != Some(&context) {
            self.composer.completion_index = 0;
            self.composer.dismissed = None;
            self.composer.completion_context = Some(context);
        }
        let keys = std::mem::take(&mut self.composer.keys);
        for key in keys {
            if let Some(key) = key.strip_prefix("shortcut:") {
                self.composer_shortcut(key, window, cx);
                continue;
            }
            if !self.completion_key(&key, window, cx) && key == "enter" {
                // Native input may change between its key event and the next
                // frame. A menu that just closed must not swallow submission.
                ui::command(json!({"action":"composer-send"}));
            }
        }
        let menu = self.completion_menu(window, cx);
        div()
            .absolute()
            .left(px(design::sidebar_width(cx) + self.assistant_pane_width(cx)))
            .right_0()
            .bottom(px(24.))
            .flex()
            .justify_center()
            .child(
                div()
                    .w(px(width))
                    .h(px(height))
                    .relative()
                    .rounded(px(16.))
                    .border_1()
                    .border_color(cx.theme().border)
                    .bg(design::surface(cx))
                    .shadow_sm()
                    .when(
                        self.editor_snapshot(cx).text.is_empty()
                            && !platform::with(|p| p.capabilities().browser_access),
                        |el| {
                            el.child(
                                div()
                                    .absolute()
                                    .left(px(18.))
                                    .right(px(if touch { 62. } else { 50. }))
                                    .truncate()
                                    .top(px(if touch { 18. } else { 12. }))
                                    .text_size(px(if touch { 16. } else { 15. }))
                                    .text_color(cx.theme().muted_foreground)
                                    .child(i18n::text(if view.selected == "new" {
                                        "bridge.describe-the-goal-of-a-new-task"
                                    } else if view.selected.is_empty() {
                                        "bridge.message-pot"
                                    } else {
                                        "bridge.add-instructions-interrupted-tasks-resume-next-turn"
                                    })),
                            )
                        },
                    )
                    .child(menu)
                    .when(!native, |el| {
                        el.child(
                            div()
                                .capture_action(cx.listener(
                                    |this, action: &gpui_kit::base::input::Enter, window, cx| {
                                        if action.shift || platform::with(|p| p.is_composing()) {
                                            return;
                                        }
                                        if !this.completion_key("enter", window, cx) {
                                            let text =
                                                this.composer.input.read(cx).value().to_string();
                                            if text.trim().is_empty() {
                                                if this
                                                    .client
                                                    .view
                                                    .lock()
                                                    .unwrap()
                                                    .snapshot
                                                    .as_ref()
                                                    .is_some_and(|s| s.turn.is_some())
                                                {
                                                    this.client.cancel();
                                                }
                                            } else {
                                                this.client.send(text);
                                            }
                                        }
                                        cx.stop_propagation();
                                        window.prevent_default();
                                    },
                                ))
                                .capture_action(cx.listener(
                                    |this, _: &gpui_kit::base::input::MoveUp, window, cx| {
                                        if this.completion_key("up", window, cx) {
                                            cx.stop_propagation();
                                            window.prevent_default();
                                        }
                                    },
                                ))
                                .capture_action(cx.listener(
                                    |this, _: &gpui_kit::base::input::MoveDown, window, cx| {
                                        if this.completion_key("down", window, cx) {
                                            cx.stop_propagation();
                                            window.prevent_default();
                                        }
                                    },
                                ))
                                .capture_action(cx.listener(
                                    |this, _: &gpui_kit::base::input::Escape, window, cx| {
                                        if this.completion_key("escape", window, cx) {
                                            cx.stop_propagation();
                                            window.prevent_default();
                                        }
                                    },
                                ))
                                .absolute()
                                .left(px(8.))
                                .right(px(if touch { 62. } else { 50. }))
                                .top(px(8.))
                                .bottom(px(8.))
                                .child(
                                    Textarea::new(&self.composer.input)
                                        .small()
                                        .line_height(px(if touch { 20. } else { 18. }))
                                        .appearance(false)
                                        .bordered(false)
                                        .text_size(px(if touch { 16. } else { 15. }))
                                        .aria_label(i18n::text("mobile.message"))
                                        .h(px(height - 16.)),
                                ),
                        )
                    })
                    .child(
                        Button::new("composer-submit")
                            .primary()
                            .w(px(if touch { 44. } else { 32. }))
                            .h(px(if touch { 44. } else { 32. }))
                            .rounded_full()
                            .absolute()
                            .right(px(8.))
                            .bottom(px(8.))
                            .icon(if retry {
                                gpui_kit::component::IconName::RefreshCw
                            } else if stop {
                                gpui_kit::component::IconName::Square
                            } else {
                                gpui_kit::component::IconName::ArrowUp
                            })
                            .accessibility_label(i18n::text(if retry {
                                "ui.retry"
                            } else if stop {
                                "mobile.stop"
                            } else {
                                "mobile.send"
                            }))
                            .disabled(!stop && !view.can_send() && !retry)
                            .on_click(cx.listener(move |this, _, _, cx| {
                                if retry {
                                    this.client.retry();
                                } else if native {
                                    ui::command(json!({"action":"composer-send"}));
                                } else {
                                    let text = this.composer.input.read(cx).value().to_string();
                                    if running && text.trim().is_empty() {
                                        this.client.cancel();
                                    } else if !text.trim().is_empty() {
                                        this.client.send(text);
                                    }
                                }
                                cx.notify();
                            })),
                    ),
            )
    }
}

impl Shell {
    fn editor_snapshot(&self, cx: &App) -> platform::EditorSnapshot {
        platform::with(|p| p.editor()).unwrap_or_else(|| {
            let input = self.composer.input.read(cx);
            platform::EditorSnapshot {
                text: input.value().to_string(),
                caret: input.cursor(),
                selection: !input.selected_range().is_empty(),
                composing: platform::with(|p| p.is_composing()),
                content_height: None,
            }
        })
    }
    fn completions(&self, cx: &App) -> Vec<pot_protocol::references::Completion> {
        let editor = self.editor_snapshot(cx);
        if editor.composing
            || editor.selection
            || self.composer.dismissed.as_ref() == Some(&(editor.text.clone(), editor.caret))
            || !self.client.view.lock().unwrap().selected.is_empty()
        {
            return vec![];
        }
        let Some((_, query)) = pot_protocol::references::query(&editor.text, editor.caret) else {
            return vec![];
        };
        let view = self.client.view.lock().unwrap();
        serde_json::from_value::<Vec<pot_protocol::references::Completion>>(
            view.completions["items"].clone(),
        )
        .unwrap_or_default()
        .into_iter()
        .filter(|i| pot_protocol::references::matches(i, query))
        .take(30)
        .collect()
    }
    fn insert_completion(&mut self, token: &str, window: &mut Window, cx: &mut Context<Self>) {
        let editor = self.editor_snapshot(cx);
        if let Some((start, _)) = pot_protocol::references::query(&editor.text, editor.caret) {
            if platform::with(|p| p.editor().is_some()) {
                ui::command(
                    json!({"action":"editor-replace","start":start,"end":editor.caret,"text":format!("{token} ")}),
                );
            } else {
                self.composer.input.update(cx, |input, cx| {
                    input.set_selected_range(start..editor.caret, cx);
                    input.replace(format!("{token} "), window, cx);
                    input.focus(window, cx);
                });
            }
        }
        self.composer.completion_index = 0;
        self.composer.dismissed = None;
    }
    fn completion_key(&mut self, key: &str, window: &mut Window, cx: &mut Context<Self>) -> bool {
        let items = self.completions(cx);
        if items.is_empty() {
            return false;
        }
        match key {
            "down" => {
                self.composer.completion_index = (self.composer.completion_index + 1) % items.len()
            }
            "up" => {
                self.composer.completion_index =
                    (self.composer.completion_index.min(items.len() - 1) + items.len() - 1)
                        % items.len()
            }
            "enter" => {
                let token = items[self.composer.completion_index.min(items.len() - 1)]
                    .token
                    .clone();
                self.insert_completion(&token, window, cx);
            }
            "escape" => {
                let editor = self.editor_snapshot(cx);
                self.composer.dismissed = Some((editor.text, editor.caret));
            }
            _ => return false,
        }
        cx.notify();
        true
    }
    fn completion_menu(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let items = self.completions(cx);
        platform::with(|p| {
            p.command(json!({"action":"completion-active","value":!items.is_empty()}))
        });
        let rows = ((f32::from(window.fully_visible_bounds().size.height) - 230.) / 34.)
            .clamp(1., 6.) as usize;
        let selected = self
            .composer
            .completion_index
            .min(items.len().saturating_sub(1));
        div()
            .absolute()
            .left_0()
            .right_0()
            .bottom(px((self.composer.height).clamp(48., 150.) + 6.))
            .bg(design::surface(cx))
            .rounded(px(8.))
            .shadow_sm()
            .flex()
            .flex_col()
            .children(
                items
                    .into_iter()
                    .enumerate()
                    .skip(selected.saturating_sub(rows - 1))
                    .take(rows)
                    .map(|(index, item)| {
                        let token = item.token.clone();
                        Button::new(("composer-completion", index))
                            .ghost()
                            .h(px(34.))
                            .label(format!(
                                "{} {} · {}",
                                if index == selected { "›" } else { " " },
                                item.label,
                                item.token.split(':').next().unwrap_or("")
                            ))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.insert_completion(&token, window, cx);
                                cx.notify();
                            }))
                    }),
            )
    }
    pub(super) fn composer_key_down(
        &mut self,
        event: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if platform::with(|p| p.native_window() || p.is_composing()) {
            return;
        }
        let primary = event.keystroke.modifiers.platform || event.keystroke.modifiers.control;
        if primary {
            let handled = self.composer_shortcut(&event.keystroke.key, window, cx);
            if handled {
                cx.stop_propagation();
                window.prevent_default();
            }
        }
    }
    pub(super) fn composer_shortcut(
        &mut self,
        key: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        match key {
            "[" => {
                self.navigate_up(window, cx);
                true
            }
            "," => {
                ui::command(json!({"action":"page","value":2}));
                true
            }
            "d" => {
                let page = if self.host["page"] == 1 { 0 } else { 1 };
                ui::command(json!({"action":"page","value":page}));
                true
            }
            "." => {
                self.client.cancel();
                true
            }
            _ => false,
        }
    }
}

impl Shell {
    pub(super) fn layout_composer(&mut self, window: &mut Window, cx: &App) {
        let visible = window.fully_visible_bounds();
        let width = (f32::from(visible.size.width)
            - (design::sidebar_width(cx) + if design::mobile(cx) { 32. } else { 64. })
            - self.assistant_pane_width(cx))
        .clamp(0., 760.);
        let touch = design::touch(cx);
        let font_size = px(if touch { 16. } else { 15. });
        let editor = self.editor_snapshot(cx);
        let content_height = editor.content_height;
        let text: SharedString = editor.text.into();
        let style = TextStyle {
            font_family: cx.theme().font_family.clone(),
            font_size: font_size.into(),
            color: cx.theme().foreground,
            ..Default::default()
        };
        let runs = [style.to_run(text.len())];
        let rows = window
            .text_system()
            .shape_text(
                text,
                font_size,
                &runs,
                Some(px((width - if touch { 86. } else { 74. }).max(1.))),
                None,
            )
            .map(|lines| {
                lines
                    .iter()
                    .map(|line| line.wrap_boundaries.len() + 1)
                    .sum::<usize>()
            })
            .unwrap_or(1);
        let browser = platform::with(|p| p.capabilities().browser_access);
        let line_height = if touch { 20. } else { 18. };
        let inset_y = if browser {
            if touch { 20. } else { 15. }
        } else {
            10.
        };
        self.composer.height = (content_height.unwrap_or(rows as f32 * line_height) + 2. * inset_y)
            .clamp(if touch { 60. } else { 48. }, 150.);
        if platform::with(|p| p.capabilities().external_text_input) {
            let selected = self.client.view.lock().unwrap().selected.clone();
            let placeholder = i18n::text(if selected == "new" {
                "bridge.describe-the-goal-of-a-new-task"
            } else if selected.is_empty() {
                "bridge.message-pot"
            } else {
                "bridge.add-instructions-interrupted-tasks-resume-next-turn"
            });
            platform::with(|p| {
                p.command(json!({"action":"editor-geometry","x":f32::from(visible.origin.x)+design::sidebar_width(cx)+self.assistant_pane_width(cx)+(f32::from(visible.size.width)-design::sidebar_width(cx)-self.assistant_pane_width(cx)-width)/2.,"y":24.,"top":f32::from(visible.origin.y+visible.size.height)-24.-self.composer.height,"width":width,"height":self.composer.height,"inset_y":inset_y,"right":if touch {62.} else {50.},"font_size":if touch {16.} else {15.},"line_height":line_height,"placeholder":placeholder,"label":i18n::text("mobile.message")}))
            });
        }
    }
}
