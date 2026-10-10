//! Control membership. Joining a machine gives Control its configured resources.
use crate::{
    localization::Catalog,
    pages::{self, Handler},
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Disableable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};
#[derive(Default)]
pub struct Editor {
    pub open: bool,
}
pub struct View<'a> {
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
        _window: &mut Window,
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
                body = body.child(pages::hint(invitation.to_owned(), cx));
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
        if let Some(error) = view.reply["error"].as_str() {
            body = body.child(pages::hint(error.to_owned(), cx));
        }
        div().child(body)
    }
}
