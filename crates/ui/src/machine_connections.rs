//! Control membership. Joining a machine gives Control its configured resources.
use crate::{
    localization::Catalog,
    pages::{self, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::input::{Input, InputEvent, InputState, Textarea, TextareaState};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};
#[derive(Default)]
pub struct Editor {
    pub open: bool,
    pub mode: Option<String>,
    pub machine: Option<String>,
    pub members: bool,
    focus: Option<FocusHandle>,
    return_focus: Option<FocusHandle>,
    presented: bool,
    fields: Option<Fields>,
    tags: std::collections::HashMap<String, Entity<TagInput>>,
}
struct Fields {
    address: Entity<InputState>,
    token: Entity<InputState>,
    ca: Entity<TextareaState>,
    advanced: Entity<CaOptions>,
}
pub struct View<'a> {
    pub client: Option<&'a Value>,
    pub directory: &'a Value,
    pub selected: &'a Value,
    pub reply: &'a Value,
    pub pending: bool,
}
impl Editor {
    pub fn connect(&mut self) {
        self.open = true;
        self.mode = None;
        self.machine = None;
        self.members = false;
    }
    pub fn edit_labels(&mut self, machine: String) {
        self.open = true;
        self.machine = Some(machine);
        self.members = false;
    }
    pub fn manage_members(&mut self) {
        self.open = true;
        self.machine = None;
        self.members = true;
    }

    pub fn render(
        &mut self,
        view: View<'_>,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        if !self.open {
            if self.presented {
                if let Some(focus) = self.return_focus.take() {
                    focus.focus(window, cx);
                }
                self.presented = false;
            }
            return div();
        }
        let focus = self.focus.get_or_insert_with(|| cx.focus_handle()).clone();
        if !self.presented {
            self.return_focus = window.focused(cx);
            focus.focus(window, cx);
            self.presented = true;
        }
        let close = handler.clone();
        let mut body = div()
            .id("machine-connection-panel")
            .w_full()
            .max_w(px(520.))
            .max_h(window.viewport_size().height - px(120.))
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_4()
            .p_6()
            .rounded(px(12.))
            .border_1()
            .border_color(cx.theme().border)
            .bg(crate::design::surface(cx))
            .shadow_lg()
            .occlude()
            .track_focus(&focus)
            .on_key_down(move |event, window, cx| {
                if event.keystroke.key == "escape" {
                    close(
                        &pages::PageAction {
                            kind: "machine-connections-toggle",
                            value: Value::Null,
                        },
                        window,
                        cx,
                    );
                    cx.stop_propagation();
                }
            })
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(pages::section(catalog.text(if self.machine.is_some() {
                        "machines.edit-tags"
                    } else if self.members {
                        "machines.add-executor"
                    } else {
                        "onboarding.connect-server"
                    })))
                    .child(pages::action_tooltip(
                        "close-machine-connections-tip",
                        catalog.text("assistant.close"),
                        Button::new("close-machine-connections")
                            .ghost()
                            .small()
                            .icon(IconName::Close)
                            .accessibility_label(catalog.text("assistant.close"))
                            .on_click(pages::on(
                                &handler,
                                "machine-connections-toggle",
                                Value::Null,
                            )),
                    )),
            );
        if let Some(error) = view.reply["error"].as_str() {
            body = body.child(pages::notice(error.to_owned(), true, cx));
        }
        if let Some(client) = view
            .client
            .filter(|_| !self.members && self.machine.is_none())
        {
            if client["configured"] == true {
                self.fields = None;
                body = body.child(pages::hint(
                    client["url"].as_str().unwrap_or("").to_owned(),
                    cx,
                ));
            } else {
                let fields = self.fields.get_or_insert_with(|| {
                    let ca = cx.new(|cx| TextareaState::new(window, cx));
                    let advanced = cx.new(|_| CaOptions {
                        open: false,
                        ca: ca.clone(),
                        catalog,
                    });
                    Fields {
                        address: cx.new(|cx| InputState::new(window, cx)),
                        token: cx.new(|cx| InputState::new(window, cx).masked(true)),
                        ca,
                        advanced,
                    }
                });
                fields
                    .advanced
                    .update(cx, |state, _| state.catalog = catalog);
                body = body
                    .child(pages::hint(catalog.text("machines.add-control-hint"), cx))
                    .child(div().child(catalog.text("machines.control-address")))
                    .child(
                        Input::new(&fields.address)
                            .aria_label(catalog.text("machines.control-address")),
                    )
                    .child(div().child(catalog.text("machines.control-token")))
                    .child(
                        Input::new(&fields.token)
                            .aria_label(catalog.text("machines.control-token")),
                    )
                    .child(fields.advanced.clone());
                let address = fields.address.clone();
                let token = fields.token.clone();
                let ca = fields.ca.clone();
                let h = handler.clone();
                body=body.child(Button::new("configure-control").primary().self_end().disabled(view.pending).label(catalog.text("machines.add-control"))
                    .on_click(move|_,window,cx|{let pem=ca.read(cx).value().to_string();h(&pages::PageAction {kind:"machine-command",value:json!({"command":{"action":"configure_control","url":address.read(cx).value().to_string(),"token":token.read(cx).value().to_string(),"ca_pem":if pem.trim().is_empty(){None}else{Some(pem)}}})},window,cx);}));
            }
        }
        let mut suggestions = pot_protocol::references::default_labels(true, "macos");
        suggestions.extend(["linux".to_owned(), "windows".to_owned()]);
        for machine in view.directory["machines"].as_array().into_iter().flatten() {
            suggestions.extend(
                machine["tags"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .filter_map(Value::as_str)
                    .map(str::to_owned),
            );
        }
        suggestions.sort();
        suggestions.dedup();
        for machine in view.directory["machines"].as_array().into_iter().flatten() {
            let Some(id) = machine["runtime_id"].as_str() else {
                continue;
            };
            if self.machine.as_deref() != Some(id) {
                continue;
            }
            let input = self
                .tags
                .entry(id.to_owned())
                .or_insert_with(|| {
                    let value = machine["tags"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .filter_map(Value::as_str)
                        .map(str::to_owned)
                        .collect::<Vec<_>>();
                    cx.new(|cx| TagInput::new(value, catalog, window, cx))
                })
                .clone();
            input.update(cx, |state, _| {
                state.catalog = catalog;
                state.suggestions = suggestions.clone();
            });
            let h = handler.clone();
            let runtime = id.to_owned();
            let field = input.clone();
            body = body.child(pages::hint(format!("{} · {}", machine["name"].as_str().unwrap_or(catalog.text("machines.local")), catalog.text("machines.tags")), cx))
                .child(input.clone())
                .child(Button::new(SharedString::from(format!("save-tags-{id}"))).small().self_end().disabled(view.pending)
                    .label(catalog.text("machines.save-tags")).on_click(move |_, window, cx| {
                        let tags = field.update(cx, |state, cx| { state.commit(true, window, cx); state.tags.clone() });
                        h(&pages::PageAction { kind: "machine-command", value: json!({"command":{"action":"set_tags","runtime_id":runtime,"tags":tags}}) }, window, cx);
                    }));
        }
        let authority = &view.directory["control_authority"];
        if self.members {
            body = body.child(pages::hint(catalog.text("machines.join-instructions"), cx));
        }
        if self.members && authority["role"] == "control" {
            body = body.child(
                Button::new("machine-invitation")
                    .ghost()
                    .disabled(view.pending)
                    .label(catalog.text("machines.create-invitation"))
                    .on_click(pages::on(
                        &handler,
                        "machine-command",
                        json!({"command":{"action":"create_invitation"}}),
                    )),
            );
            if let Some(invitation) = view.reply["result"]["invitation"].as_str() {
                let copy = invitation.to_owned();
                body = body.child(pages::hint(invitation.to_owned(), cx)).child(
                    Button::new("copy-invitation")
                        .ghost()
                        .label(catalog.text("machines.copy-invitation"))
                        .on_click(move |_, _, cx| {
                            cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()))
                        }),
                );
            }
            for member in authority["members"].as_array().cloned().unwrap_or_default() {
                if member["enabled"] != true {
                    continue;
                }
                body=body.child(pages::hint(format!("{} · {}",member["identity"]["runtime_id"].as_str().unwrap_or(""),member["identity"]["fingerprint"].as_str().unwrap_or("")),cx))
                    .child(Button::new(SharedString::from(format!("remove-member-{}",member["identity"]["runtime_id"]))).ghost().disabled(view.pending)
                        .label(catalog.text("machines.remove-member")).on_click(pages::on(&handler,"machine-command",json!({"command":{"action":"remove_member","runtime_id":member["identity"]["runtime_id"]}}))));
            }
        } else if let Some(url) = authority["control"]["url"]
            .as_str()
            .filter(|_| self.members)
        {
            body = body.child(pages::hint(url.to_owned(), cx));
        }
        div()
            .absolute()
            .inset_0()
            .size_full()
            .bg(gpui::black().opacity(0.12))
            .flex()
            .items_start()
            .justify_center()
            .pt_8()
            .px_4()
            .occlude()
            .child(body)
    }
}

struct CaOptions {
    open: bool,
    ca: Entity<TextareaState>,
    catalog: Catalog,
}
impl Render for CaOptions {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .flex()
            .flex_col()
            .gap_3()
            .child(
                Button::new("advanced-connection")
                    .ghost()
                    .self_start()
                    .icon(if self.open {
                        IconName::ChevronDown
                    } else {
                        IconName::ChevronRight
                    })
                    .label(self.catalog.text("machines.advanced-connection"))
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.open = !this.open;
                        cx.notify();
                    })),
            )
            .when(self.open, |el| {
                el.child(div().child(self.catalog.text("machines.control-ca")))
                    .child(
                        Textarea::new(&self.ca)
                            .h(px(70.))
                            .aria_label(self.catalog.text("machines.control-ca")),
                    )
            })
    }
}

struct TagInput {
    suggestions: Vec<String>,
    focused: bool,
    dismissed: bool,
    highlighted: usize,
    tags: Vec<String>,
    input: Entity<InputState>,
    catalog: Catalog,
    _subscription: Subscription,
}
impl TagInput {
    fn new(
        tags: Vec<String>,
        catalog: Catalog,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let input = cx.new(|cx| InputState::new(window, cx));
        let subscription = cx.subscribe_in(
            &input,
            window,
            |this, _, event: &InputEvent, window, cx| match event {
                InputEvent::Change => {
                    this.dismissed = false;
                    this.highlighted = 0;
                    this.commit(false, window, cx);
                    cx.notify();
                }
                InputEvent::PressEnter { .. } => this.commit(true, window, cx),
                InputEvent::Blur => {
                    this.focused = false;
                    this.commit(true, window, cx);
                    cx.notify();
                }
                InputEvent::Focus => {
                    this.focused = true;
                    this.dismissed = false;
                    cx.notify();
                }
            },
        );
        Self {
            suggestions: Vec::new(),
            focused: false,
            dismissed: false,
            highlighted: 0,
            tags,
            input,
            catalog,
            _subscription: subscription,
        }
    }
    fn matches(&self, cx: &App) -> Vec<String> {
        let query = self.input.read(cx).value().trim().to_ascii_lowercase();
        if !self.focused || self.dismissed || query.is_empty() {
            return Vec::new();
        }
        let mut matches: Vec<_> = self
            .suggestions
            .iter()
            .filter(|tag| !self.tags.contains(tag) && tag.contains(&query))
            .cloned()
            .collect();
        matches.sort_by_key(|tag| (!tag.starts_with(&query), tag.clone()));
        matches.truncate(8);
        matches
    }
    fn choose(&mut self, tag: String, window: &mut Window, cx: &mut Context<Self>) {
        if !self.tags.contains(&tag) {
            self.tags.push(tag);
        }
        self.input.update(cx, |input, cx| {
            input.set_value("", window, cx);
            input.focus(window, cx);
        });
        self.highlighted = 0;
        cx.notify();
    }
    fn commit(&mut self, all: bool, window: &mut Window, cx: &mut Context<Self>) {
        if self.input.update(cx, |input, cx| {
            input.marked_text_range(window, cx).is_some()
        }) {
            return;
        }
        let text = self.input.read(cx).value().to_string();
        let end = if all {
            text.len()
        } else {
            text.char_indices()
                .filter(|(_, c)| c.is_whitespace() || *c == ',')
                .map(|(i, c)| i + c.len_utf8())
                .last()
                .unwrap_or(0)
        };
        if end == 0 {
            return;
        }
        for tag in text[..end]
            .split(|c: char| c.is_whitespace() || c == ',')
            .filter(|t| !t.is_empty())
        {
            if !self.tags.iter().any(|t| t == tag) {
                self.tags.push(tag.to_owned());
            }
        }
        self.input.update(cx, |input, cx| {
            input.set_value(text[end..].to_owned(), window, cx)
        });
        cx.notify();
    }
}
impl Render for TagInput {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let matches = self.matches(cx);
        let input = div()
            .id("tag-input")
            .flex()
            .flex_wrap()
            .items_center()
            .gap_2()
            .p_2()
            .border_1()
            .border_color(cx.theme().border)
            .rounded(px(8.))
            .capture_key_down(cx.listener(|this, event: &KeyDownEvent, window, cx| {
                if this.input.update(cx, |input, cx| {
                    input.marked_text_range(window, cx).is_some()
                }) {
                    return;
                }
                let matches = this.matches(cx);
                if !matches.is_empty() {
                    match event.keystroke.key.as_str() {
                        "down" => {
                            this.highlighted = (this.highlighted + 1) % matches.len();
                        }
                        "up" => {
                            this.highlighted =
                                (this.highlighted + matches.len() - 1) % matches.len();
                        }
                        "enter" => {
                            this.choose(
                                matches[this.highlighted.min(matches.len() - 1)].clone(),
                                window,
                                cx,
                            );
                        }
                        "escape" => {
                            this.dismissed = true;
                        }
                        _ => return,
                    }
                    cx.notify();
                    cx.stop_propagation();
                    return;
                }
                if event.keystroke.key == "backspace" && this.input.read(cx).value().is_empty() {
                    this.tags.pop();
                    cx.notify();
                    cx.stop_propagation();
                }
            }))
            .children(self.tags.iter().enumerate().map(|(index, tag)| {
                div()
                    .flex()
                    .items_center()
                    .gap_1()
                    .pl_2()
                    .pr_1()
                    .py_1()
                    .rounded(px(6.))
                    .bg(cx.theme().primary.opacity(0.1))
                    .text_color(cx.theme().primary)
                    .child(tag.clone())
                    .child(
                        Button::new(("remove-tag", index))
                            .ghost()
                            .xsmall()
                            .icon(IconName::Close)
                            .accessibility_label(
                                self.catalog.format("machines.remove-tag", &[tag.clone()]),
                            )
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.tags.remove(index);
                                this.input.update(cx, |input, cx| input.focus(window, cx));
                                cx.notify();
                            })),
                    )
            }))
            .child(
                div().min_w(px(120.)).flex_1().child(
                    Input::new(&self.input)
                        .appearance(false)
                        .aria_label(self.catalog.text("machines.tags")),
                ),
            );
        div()
            .flex()
            .flex_col()
            .gap_1()
            .child(input)
            .when(!matches.is_empty(), |el| {
                el.child(
                    div()
                        .id("tag-suggestions")
                        .flex()
                        .flex_col()
                        .p_1()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(cx.theme().border)
                        .bg(crate::design::surface(cx))
                        .shadow_sm()
                        .children(matches.into_iter().enumerate().map(|(index, tag)| {
                            let choice = tag.clone();
                            let keyboard_choice = tag.clone();
                            div()
                                .id(("tag-suggestion", index))
                                .px_3()
                                .py_2()
                                .rounded(px(5.))
                                .cursor_pointer()
                                .when(index == self.highlighted, |el| {
                                    el.bg(cx.theme().primary.opacity(0.1))
                                })
                                .hover(|el| el.bg(cx.theme().primary.opacity(0.15)))
                                .child(
                                    Button::new(("tag-choice", index))
                                        .ghost()
                                        .w_full()
                                        .accessibility_label(tag.clone())
                                        .child(div().w_full().child(tag))
                                        .on_click(cx.listener(move |this, _, window, cx| {
                                            this.choose(keyboard_choice.clone(), window, cx);
                                        })),
                                )
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |this, _, window, cx| {
                                        this.choose(choice.clone(), window, cx);
                                        cx.stop_propagation();
                                    }),
                                )
                        })),
                )
            })
    }
}
