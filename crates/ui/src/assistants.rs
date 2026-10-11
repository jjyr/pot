//! Shared assistant selector/editor; shells provide I/O and retain drafts by owner.
use crate::{
    localization::Catalog,
    pages::{self, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Sizable,
    button::{Button, ButtonVariants},
    input::{Input, InputState, Textarea, TextareaState},
};
use serde_json::{Value, json};
use std::collections::HashMap;
struct Draft {
    name: Entity<InputState>,
    rules: Entity<TextareaState>,
    revision: Value,
    rules_revision: Value,
    request_id: String,
}
#[derive(Default)]
pub struct Editor {
    pub open: bool,
    selected: String,
    saved_owner: String,
    drafts: HashMap<String, Draft>,
}
pub struct View<'a> {
    pub assistants: &'a Value,
    pub current: &'a str,
    pub rules: &'a Value,
}
impl Editor {
    pub fn select(&mut self, owner: &str) {
        self.selected = owner.into();
        self.open = true;
    }
    pub fn render(
        &mut self,
        view: View<'_>,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        let View {
            assistants,
            current,
            rules,
        } = view;
        let list = assistants["assistants"]
            .as_array()
            .cloned()
            .unwrap_or_default();
        let name = list
            .iter()
            .find(|d| d["id"] == current)
            .and_then(|d| d["name"].as_str())
            .unwrap_or("Pot");
        let mut body = div().flex().flex_col().gap_3().p_4().child(
            Button::new("assistant-name")
                .ghost()
                .label(name.to_owned())
                .on_click(pages::on(&handler, "assistant-edit", json!(current))),
        );
        if !self.open {
            return body;
        }
        body = body.child(
            div()
                .flex()
                .flex_wrap()
                .gap_2()
                .children(list.iter().map(|d| {
                    Button::new(SharedString::from(format!(
                        "assistant-{}",
                        d["id"].as_str().unwrap_or_default()
                    )))
                    .ghost()
                    .small()
                    .label(d["name"].as_str().unwrap_or_default().to_owned())
                    .toggled(d["id"] == current)
                    .on_click(pages::on(
                        &handler,
                        "assistant-select",
                        d["id"].clone(),
                    ))
                }))
                .child(
                    Button::new("assistant-new")
                        .ghost()
                        .small()
                        .label(catalog.text("assistant.new"))
                        .on_click(pages::on(&handler, "assistant-new", Value::Null)),
                ),
        );
        let owner = if self.selected.is_empty() {
            current
        } else {
            &self.selected
        }
        .to_owned();
        if owner != "new" && (owner != current || rules["revision"].is_null()) {
            return body;
        }
        if !self.drafts.contains_key(&owner) {
            let definition = list.iter().find(|d| d["id"] == owner);
            let text = definition
                .and_then(|d| d["name"].as_str())
                .unwrap_or_default()
                .to_owned();
            let name = cx.new(|cx| InputState::new(window, cx));
            name.update(cx, |f, cx| f.set_value(text, window, cx));
            let field = cx.new(|cx| TextareaState::new(window, cx));
            field.update(cx, |f, cx| {
                f.set_value(rules["text"].as_str().unwrap_or_default(), window, cx)
            });
            self.drafts.insert(
                owner.clone(),
                Draft {
                    name,
                    rules: field,
                    revision: definition
                        .map(|d| d["revision"].clone())
                        .unwrap_or(Value::Null),
                    rules_revision: rules["revision"].clone(),
                    request_id: uuid::Uuid::new_v4().to_string(),
                },
            );
        }
        let draft = &self.drafts[&owner];
        let conflict = owner != "new"
            && (draft.rules_revision != rules["revision"]
                || list
                    .iter()
                    .find(|d| d["id"] == owner)
                    .is_some_and(|d| draft.revision != d["revision"]));
        if conflict {
            body = body
                .child(pages::hint(catalog.text("assistant.conflict"), cx))
                .child(
                    Button::new("assistant-reload")
                        .ghost()
                        .label(catalog.text("assistant.reload"))
                        .on_click(pages::on(&handler, "assistant-reload", json!(owner))),
                );
        } else if self.saved_owner == owner {
            body = body.child(pages::hint(catalog.text("assistant.saved"), cx));
        }

        let request_id = draft.request_id.clone();
        let save_name = draft.name.clone();
        let save_rules = draft.rules.clone();
        let save_revision = draft.revision.clone();
        let save_rules_revision = draft.rules_revision.clone();
        let save_owner = owner.clone();
        let save_handler = handler.clone();

        body = body.child(
            pages::card(cx)
                .flex()
                .flex_col()
                .gap_3()
                .child(div().child(catalog.text("assistant.name")))
                .child(Input::new(&draft.name))
                .when(owner != "new", |el| {
                    el.child(div().child("AGENTS.md"))
                        .child(Textarea::new(&draft.rules).h(px(220.)))
                        .child(pages::hint(catalog.text("assistant.rules-hint"), cx))
                })
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(
                            Button::new("assistant-save")
                                .primary()
                                .label(catalog.text("assistant.save"))
                                .on_click(move |_, window, cx| {
                                    save_handler(&pages::PageAction { kind: "assistant-save", value: json!({"request_id":request_id,"owner":save_owner,"name":save_name.read(cx).value().to_string(),"text":save_rules.read(cx).value().to_string(),"expected_revision":save_revision,"rules_revision":save_rules_revision}) }, window, cx);
                                }),
                        )
                        .when(owner != "new", |el| {
                            el.child(
                                Button::new("assistant-model")
                                    .ghost()
                                    .label(catalog.text("assistant.model"))
                                    .on_click(pages::on(&handler, "assistant-model", json!(owner))),
                            )
                        })
                        .child(
                            Button::new("assistant-close")
                                .ghost()
                                .label(catalog.text("assistant.close"))
                                .on_click(pages::on(&handler, "assistant-close", Value::Null)),
                        ),
                ),
        );
        body
    }
    pub fn saved(&mut self, owner: &str) {
        self.drafts.remove(owner);
        self.saved_owner = owner.into();
    }
    pub fn reload(&mut self, owner: &str) {
        self.drafts.remove(owner);
        self.saved_owner.clear();
    }
}
