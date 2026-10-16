//! Assistant-owned configuration forms; Provider definitions come from the owning Space.
use crate::{
    localization::Catalog,
    pages::{self, Control, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Disableable,
    button::{Button, ButtonVariants},
    input::{Input, InputState, Textarea, TextareaState},
    menu::{DropdownMenu, PopupMenuItem},
};
use serde_json::{Value, json};
use std::collections::HashMap;

struct Draft {
    scope: String,
    name: Entity<InputState>,
    rules: Entity<TextareaState>,
    model: Entity<InputState>,
    context: Entity<InputState>,
    output: Entity<InputState>,
    effort: Entity<InputState>,
    provider: String,
    revision: Value,
    rules_revision: Value,
    request_id: String,
    instructions_open: bool,
    parameters_open: bool,
}
fn input(value: &str, window: &mut Window, cx: &mut App) -> Entity<InputState> {
    cx.new(|cx| InputState::new(window, cx).default_value(value))
}
impl Draft {
    fn new(
        scope: &str,
        name: &str,
        definition: &Value,
        rules: &Value,
        selection: &Value,
        window: &mut Window,
        cx: &mut App,
    ) -> Self {
        let rules_input = cx.new(|cx| TextareaState::new(window, cx));
        rules_input.update(cx, |f, cx| {
            f.set_value(rules["text"].as_str().unwrap_or(""), window, cx)
        });
        let number = |key: &str| {
            selection[key]
                .as_u64()
                .map(|n| n.to_string())
                .unwrap_or_default()
        };
        Self {
            scope: scope.into(),
            name: input(name, window, cx),
            rules: rules_input,
            model: input(selection["model"].as_str().unwrap_or(""), window, cx),
            context: input(&number("context_window"), window, cx),
            output: input(&number("max_output_tokens"), window, cx),
            effort: input(
                selection["reasoning_effort"].as_str().unwrap_or(""),
                window,
                cx,
            ),
            provider: selection["provider_id"].as_str().unwrap_or("").into(),
            revision: definition["revision"].clone(),
            rules_revision: rules["revision"].clone(),
            request_id: uuid::Uuid::new_v4().to_string(),
            instructions_open: false,
            parameters_open: false,
        }
    }
    fn configuration(&self, cx: &App) -> Value {
        // Keep invalid input in the request so the server rejects it rather than silently inheriting.
        let number = |f: &Entity<InputState>| {
            let text = f.read(cx).value();
            if text.trim().is_empty() {
                Value::Null
            } else {
                text.trim()
                    .parse::<u64>()
                    .map(|n| json!(n))
                    .unwrap_or_else(|_| json!(text.to_string()))
            }
        };
        json!({"provider_id":self.provider,"model":self.model.read(cx).value().to_string(),"context_window":number(&self.context),"max_output_tokens":number(&self.output),"reasoning_effort":self.effort.read(cx).value().to_string()})
    }
    fn values(&self, cx: &App) -> Value {
        json!({"name":self.name.read(cx).value().to_string(),"rules":self.rules.read(cx).value().to_string(),"configuration":self.configuration(cx)})
    }
    fn payload(&self, owner: &str, cx: &App) -> Value {
        json!({"owner":owner,"name":self.name.read(cx).value().to_string(),"text":self.rules.read(cx).value().to_string(),"configuration":self.configuration(cx),"request_id":self.request_id,"expected_revision":self.revision,"rules_revision":self.rules_revision})
    }
}
#[derive(Default)]
pub struct Editor {
    creation: Option<Draft>,
    pub open: bool,
    pub form_only: bool,
    selected: String,
    selected_key: String,
    drafts: HashMap<String, Draft>,
}
pub struct View<'a> {
    pub assistants: &'a Value,
    pub current: &'a str,
    pub rules: &'a Value,
    pub settings: &'a Value,
    pub pending: bool,
    pub scope: &'a str,
}
pub struct CreateView<'a> {
    pub settings: &'a Value,
    pub source: &'a str,
    pub pending: bool,
    pub can_close: bool,
    pub location: Option<Div>,
}
impl Editor {
    pub fn creating(&self) -> bool {
        self.selected == "new"
    }
    pub fn select(&mut self, owner: &str) {
        self.selected = owner.into();
        self.open = true;
    }
    fn draft_mut(&mut self) -> Option<&mut Draft> {
        if self.selected == "new" {
            self.creation.as_mut()
        } else {
            self.drafts.get_mut(&self.selected_key)
        }
    }
    pub fn choose_provider(&mut self, value: &Value, window: &mut Window, cx: &mut App) {
        if let Some(draft) = self.draft_mut() {
            draft.provider = value["id"].as_str().unwrap_or("").into();
            draft.model.update(cx, |f, cx| {
                f.set_value(value["model"].as_str().unwrap_or(""), window, cx)
            });
            for field in [&draft.context, &draft.output, &draft.effort] {
                field.update(cx, |f, cx| f.set_value("", window, cx));
            }
        }
    }
    pub fn toggle(&mut self, section: &str) {
        if let Some(draft) = self.draft_mut() {
            match section {
                "instructions" => draft.instructions_open = !draft.instructions_open,
                "parameters" => draft.parameters_open = !draft.parameters_open,
                _ => {}
            }
        }
    }
    pub fn render(
        &mut self,
        view: View<'_>,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        let owner = view.current;
        let key = format!("{}/{}", view.scope, owner);
        self.selected_key = key.clone();
        if view.settings["ai_busy"] == true && !self.drafts.contains_key(&key) {
            return div();
        }
        if view.rules["revision"].is_null()
            || view.settings["active_spec"]["model"].is_null() && view.settings["ai_busy"] == true
        {
            return div();
        }
        let definition = view.assistants["assistants"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|d| d["id"] == owner)
            .cloned()
            .unwrap_or(Value::Null);
        self.drafts.entry(key.clone()).or_insert_with(|| {
            let mut selection=view.settings["selection"].clone();
            if selection.is_null() { selection=json!({"provider_id":view.settings["active_spec"]["source_profile_id"],"model":view.settings["active_spec"]["model"]}); }
            Draft::new(owner,definition["name"].as_str().unwrap_or("Pot"),&definition,view.rules,&selection,window,cx)
        });
        let draft = &self.drafts[&key];
        let conflict = draft.revision != definition["revision"]
            || draft.rules_revision != view.rules["revision"];
        let mut body = div().flex().flex_col().gap_3();
        if conflict {
            body = body
                .child(pages::hint(catalog.text("assistant.conflict"), cx))
                .child(
                    Button::new("assistant-reload")
                        .ghost()
                        .label(catalog.text("assistant.reload"))
                        .on_click(pages::on(&handler, "assistant-reload", json!(owner))),
                );
        }
        body.child(render_form(
            draft,
            FormView {
                owner,
                settings: view.settings,
                busy: view.pending || conflict,
                can_close: true,
                location: None,
            },
            catalog,
            handler,
            cx,
        ))
    }
    pub fn render_create(
        &mut self,
        view: CreateView<'_>,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        self.selected = "new".into();
        if view.settings["ai_busy"] == true
            && self
                .creation
                .as_ref()
                .is_none_or(|draft| draft.scope != view.source)
        {
            return div();
        }
        if let Some(draft) = self.creation.as_mut()
            && draft.scope != view.source
        {
            draft.scope = view.source.into();
            let provider = view.settings["profiles"]
                .as_array()
                .into_iter()
                .flatten()
                .find(|p| p["id"] == view.settings["default_provider_id"]);
            draft.provider = provider.and_then(|p| p["id"].as_str()).unwrap_or("").into();
            draft.model.update(cx, |f, cx| {
                f.set_value(
                    provider.and_then(|p| p["model"].as_str()).unwrap_or(""),
                    window,
                    cx,
                )
            });
            for field in [&draft.context, &draft.output, &draft.effort] {
                field.update(cx, |f, cx| f.set_value("", window, cx));
            }
        }
        let draft = self.creation.get_or_insert_with(|| {
            let default = view.settings["default_provider_id"].as_str().unwrap_or("");
            let provider = view.settings["profiles"]
                .as_array()
                .into_iter()
                .flatten()
                .find(|p| p["id"] == default);
            let selection = provider
                .map(|p| json!({"provider_id":p["id"],"model":p["model"]}))
                .unwrap_or(Value::Null);
            Draft::new(
                view.source,
                "Pot",
                &Value::Null,
                &Value::Null,
                &selection,
                window,
                cx,
            )
        });
        div()
            .flex()
            .flex_col()
            .gap_4()
            .child(avatar(&draft.request_id, 56., cx))
            .child(render_form(
                draft,
                FormView {
                    owner: "new",
                    settings: view.settings,
                    busy: view.pending,
                    can_close: view.can_close,
                    location: view.location,
                },
                catalog,
                handler,
                cx,
            ))
    }
    pub fn editor_values(&self, cx: &App) -> Value {
        if self.selected == "new" {
            self.creation.as_ref()
        } else {
            self.drafts.get(&self.selected_key)
        }
        .map(|d| d.values(cx))
        .unwrap_or(Value::Null)
    }
    pub fn close(&mut self, discard: bool) {
        if discard {
            self.drafts.remove(&self.selected_key);
            if self.selected == "new" {
                self.creation = None;
            }
        }
        self.open = false;
    }
    pub fn saved(&mut self, owner: &str) {
        if owner == "new" {
            self.creation = None;
        }
        if self.selected == owner {
            self.drafts.remove(&self.selected_key);
        }
    }
    pub fn reload(&mut self, owner: &str) {
        if self.selected == owner {
            self.drafts.remove(&self.selected_key);
        }
    }
}
struct FormView<'a> {
    owner: &'a str,
    settings: &'a Value,
    busy: bool,
    can_close: bool,
    location: Option<Div>,
}
fn render_form(
    draft: &Draft,
    view: FormView<'_>,
    catalog: Catalog,
    handler: Handler,
    cx: &App,
) -> Div {
    let FormView {
        owner,
        settings,
        busy,
        can_close,
        location,
    } = view;
    let providers = settings["profiles"].as_array().cloned().unwrap_or_default();
    let provider = providers.iter().find(|p| p["id"] == draft.provider);
    let provider_label = provider
        .and_then(|p| p["name"].as_str())
        .unwrap_or(catalog.text("assistant.provider"));
    let provider_handler = handler.clone();
    let selected = draft.provider.clone();
    let connection = settings["connections"]
        .as_array()
        .into_iter()
        .flatten()
        .find(|p| p["profile_id"] == draft.provider);
    let brand = connection.and_then(|p| p["model_settings"]["catalog_provider"].as_str());
    let mut models: Vec<String> = settings["directory"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|p| p["id"].as_str() == brand)
        .flat_map(|p| p["models"].as_array().into_iter().flatten())
        .filter_map(|m| m["id"].as_str().map(str::to_owned))
        .collect();
    if let Some(p) = provider.and_then(|p| p["model"].as_str()) {
        models.push(p.into());
    }
    if let Some(connection) = connection {
        models.extend(
            connection["models"]
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(|m| m.as_str().map(str::to_owned)),
        );
    }
    models.push(draft.model.read(cx).value().to_string());
    models.retain(|s| !s.is_empty());
    models.sort();
    models.dedup();
    let model_input = draft.model.clone();
    let mut body = div()
        .w_full()
        .flex()
        .flex_col()
        .gap_3()
        .child(div().child(catalog.text("assistant.name")))
        .child(Input::new(&draft.name).control(cx).disabled(busy))
        .when_some(location, |body, location| body.child(location))
        .child(div().child(catalog.text("assistant.provider")))
        .child(
            Button::new("assistant-provider")
                .control(cx)
                .label(provider_label.to_owned())
                .dropdown_caret(true)
                .disabled(busy)
                .dropdown_menu(move |menu, _, _| {
                    providers.iter().fold(menu, |menu, p| {
                        let value = json!({"id":p["id"],"model":p["model"]});
                        menu.item(
                            PopupMenuItem::new(p["name"].as_str().unwrap_or("").to_owned())
                                .checked(p["id"] == selected)
                                .on_click(pages::on(
                                    &provider_handler,
                                    "assistant-provider",
                                    value,
                                )),
                        )
                    })
                }),
        )
        .child(div().child(catalog.text("ai.model")))
        .child(
            div()
                .flex()
                .gap_2()
                .child(Input::new(&draft.model).control(cx).disabled(busy))
                .child(
                    Button::new("assistant-model-picker")
                        .control(cx)
                        .label(catalog.text("assistant.choose-model"))
                        .dropdown_caret(true)
                        .disabled(busy)
                        .dropdown_menu(move |menu, _, _| {
                            models.iter().fold(menu, |menu, id| {
                                let id = id.clone();
                                let input = model_input.clone();
                                menu.item(PopupMenuItem::new(id.clone()).on_click(
                                    move |_, window, cx| {
                                        input
                                            .update(cx, |f, cx| f.set_value(id.clone(), window, cx))
                                    },
                                ))
                            })
                        }),
                ),
        )
        .child(
            Button::new("assistant-add-provider")
                .ghost()
                .control(cx)
                .label(catalog.text("assistant.add-provider"))
                .disabled(busy)
                .on_click(pages::on(&handler, "assistant-add-provider", Value::Null)),
        );
    body = body.child(
        Button::new("assistant-parameters")
            .ghost()
            .control(cx)
            .label(format!(
                "{} {}",
                if draft.parameters_open { "v" } else { ">" },
                catalog.text("assistant.parameters")
            ))
            .on_click(pages::on(
                &handler,
                "assistant-disclosure",
                json!("parameters"),
            )),
    );
    if draft.parameters_open {
        body = body.child(pages::hint(
            catalog.text("assistant.parameter-defaults"),
            cx,
        ));
        for (label, field) in [
            ("ai.context-window", &draft.context),
            ("ai.max-output-tokens", &draft.output),
            ("ai.reasoning-effort", &draft.effort),
        ] {
            body = body
                .child(div().child(catalog.text(label)))
                .child(Input::new(field).control(cx).disabled(busy));
        }
    }
    body = body.child(
        Button::new("assistant-instructions")
            .ghost()
            .control(cx)
            .label(format!(
                "{} {}",
                if draft.instructions_open { "v" } else { ">" },
                catalog.text("assistant.instructions")
            ))
            .on_click(pages::on(
                &handler,
                "assistant-disclosure",
                json!("instructions"),
            )),
    );
    if draft.instructions_open {
        body = body
            .child(div().child("AGENTS.md"))
            .child(Textarea::new(&draft.rules).h(px(220.)))
            .child(pages::hint(catalog.text("assistant.rules-hint"), cx));
    }
    let payload = draft.payload(owner, cx);
    let name = draft.name.clone();
    let rules = draft.rules.clone();
    let model = draft.model.clone();
    let context = draft.context.clone();
    let output = draft.output.clone();
    let effort = draft.effort.clone();
    let save = handler.clone();
    let mut actions = div().flex().flex_wrap().gap_2();
    if can_close {
        actions = actions.child(
            Button::new("assistant-close")
                .ghost()
                .control(cx)
                .label(catalog.text("assistant.close"))
                .disabled(busy)
                .on_click(pages::on(&handler, "assistant-close", Value::Null)),
        );
    }
    body.child(
        actions.child(
            Button::new("assistant-save")
                .primary()
                .control(cx)
                .label(catalog.text(if owner == "new" {
                    "assistant.create"
                } else {
                    "assistant.save"
                }))
                .disabled(
                    busy || draft.provider.is_empty()
                        || draft.model.read(cx).value().trim().is_empty(),
                )
                .on_click(move |_, window, cx| {
                    let mut payload = payload.clone();
                    payload["name"] = json!(name.read(cx).value().to_string());
                    payload["text"] = json!(rules.read(cx).value().to_string());
                    payload["configuration"]["model"] = json!(model.read(cx).value().to_string());
                    for (key, field) in
                        [("context_window", &context), ("max_output_tokens", &output)]
                    {
                        let text = field.read(cx).value();
                        payload["configuration"][key] = if text.trim().is_empty() {
                            Value::Null
                        } else {
                            text.trim()
                                .parse::<u64>()
                                .map(|n| json!(n))
                                .unwrap_or_else(|_| json!(text.to_string()))
                        };
                    }
                    payload["configuration"]["reasoning_effort"] =
                        json!(effort.read(cx).value().to_string());
                    save(
                        &pages::PageAction {
                            kind: "assistant-save",
                            value: payload.clone(),
                        },
                        window,
                        cx,
                    );
                }),
        ),
    )
}
pub fn avatar(key: &str, size: f32, cx: &mut App) -> Div {
    crate::avatar::default_image(key, size, cx)
}
