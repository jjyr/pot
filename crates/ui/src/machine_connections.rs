//! Control membership. Joining a machine gives Control its configured resources.
use crate::{
    localization::Catalog,
    pages::{self, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::input::{Input, InputState, Textarea, TextareaState};
use gpui_kit::component::{
    Disableable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};
#[derive(Default)]
pub struct Editor {
    pub open: bool,
    fields: Option<Fields>,
    tags: std::collections::HashMap<String, Entity<InputState>>,
}
struct Fields {
    address: Entity<InputState>,
    token: Entity<InputState>,
    ca: Entity<TextareaState>,
}
pub struct View<'a> {
    pub client: Option<&'a Value>,
    pub directory: &'a Value,
    pub selected: &'a Value,
    pub reply: &'a Value,
    pub pending: bool,
}
impl Editor {
    pub fn render(
        &mut self,
        view: View<'_>,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        let mut body = div()
            .id("machine-connection-panel")
            .max_h(px(340.))
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_3()
            .p_4()
            .child(
                Button::new("machine-connections")
                    .ghost()
                    .label(catalog.text("machines.connections"))
                    .on_click(pages::on(
                        &handler,
                        "machine-connections-toggle",
                        Value::Null,
                    )),
            );
        if !self.open {
            return div().child(body);
        }
        if let Some(error) = view.reply["error"].as_str() {
            body = body.child(pages::hint(error.to_owned(), cx));
        }
        if let Some(client) = view.client {
            if client["configured"] == true {
                self.fields = None;
                body = body.child(pages::hint(
                    client["url"].as_str().unwrap_or("").to_owned(),
                    cx,
                ));
                if client["mode"] == "local" {
                    body = body.child(
                        Button::new("control-chat")
                            .primary()
                            .label(catalog.text("onboarding.server-chat"))
                            .on_click(pages::on(&handler, "control-chat", Value::Null)),
                    );
                }
            } else {
                let fields = self.fields.get_or_insert_with(|| Fields {
                    address: cx.new(|cx| InputState::new(window, cx)),
                    token: cx.new(|cx| InputState::new(window, cx).masked(true)),
                    ca: cx.new(|cx| TextareaState::new(window, cx)),
                });
                body = body
                    .child(pages::hint(catalog.text("machines.add-control-hint"), cx))
                    .child(div().child(catalog.text("machines.control-address")))
                    .child(Input::new(&fields.address))
                    .child(div().child(catalog.text("machines.control-token")))
                    .child(Input::new(&fields.token))
                    .child(div().child(catalog.text("machines.control-ca")))
                    .child(Textarea::new(&fields.ca).h(px(70.)));
                let address = fields.address.clone();
                let token = fields.token.clone();
                let ca = fields.ca.clone();
                let h = handler.clone();
                body=body.child(Button::new("configure-control").primary().disabled(view.pending).label(catalog.text("machines.add-control"))
                    .on_click(move|_,window,cx|{let pem=ca.read(cx).value().to_string();h(&pages::PageAction {kind:"machine-command",value:json!({"command":{"action":"configure_control","url":address.read(cx).value().to_string(),"token":token.read(cx).value().to_string(),"ca_pem":if pem.trim().is_empty(){None}else{Some(pem)}}})},window,cx);}));
            }
        }
        for machine in view.directory["machines"].as_array().into_iter().flatten() {
            let Some(id) = machine["runtime_id"].as_str() else {
                continue;
            };
            let input = self
                .tags
                .entry(id.to_owned())
                .or_insert_with(|| {
                    let value = machine["tags"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .filter_map(Value::as_str)
                        .collect::<Vec<_>>()
                        .join(" ");
                    cx.new(|cx| InputState::new(window, cx).default_value(value))
                })
                .clone();
            let h = handler.clone();
            let runtime = id.to_owned();
            let field = input.clone();
            body = body.child(pages::hint(format!("{} · {}", id, catalog.text("machines.tags")), cx))
                .child(Input::new(&input))
                .child(Button::new(SharedString::from(format!("save-tags-{id}"))).ghost().disabled(view.pending)
                    .label(catalog.text("machines.save-tags")).on_click(move |_, window, cx| {
                        let tags = field.read(cx).value().split_whitespace().map(str::to_owned).collect::<Vec<_>>();
                        h(&pages::PageAction { kind: "machine-command", value: json!({"command":{"action":"set_tags","runtime_id":runtime,"tags":tags}}) }, window, cx);
                    }));
        }
        let authority = &view.directory["control_authority"];
        body = body.child(pages::hint(catalog.text("machines.control-hint"), cx));
        if authority["role"] == "control" {
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
        } else if let Some(url) = authority["control"]["url"].as_str() {
            body = body.child(pages::hint(url.to_owned(), cx));
        }
        div().child(body)
    }
}
