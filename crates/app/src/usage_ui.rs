use crate::{Shell, client, gpui, ui};
use gpui::prelude::*;
use gpui_kit::component::{
    ActiveTheme, Sizable,
    button::{Button, ButtonVariants},
};
pub use pot_ui::usage::UsageUI;
pub use pot_ui::usage_state::count;
use serde_json::{Value, json};
fn n(v: &Value, k: &str) -> u64 {
    v[k].as_u64().unwrap_or(0)
}
fn tokens(v: &Value) -> u64 {
    n(v, "input") + n(v, "output")
}
pub fn entry(v: &client::View, cx: &mut Context<Shell>) -> Button {
    Button::new("conversation-usage")
        .ghost()
        .small()
        .icon(gpui_kit::component::IconName::ChartPie)
        .text_color(cx.theme().muted_foreground)
        .label(if v.usage.is_null() {
            crate::i18n::text("navigation.usage").into()
        } else {
            format!("{} tokens", count(tokens(&v.usage["lifetime"])))
        })
        .tooltip(crate::i18n::text(
            "usage-ui.view-total-usage-for-this-conversation",
        ))
        .on_click(cx.listener(|this, _, _, cx| {
            this.usage_ui.open_settings();
            ui::command(json!({"action":"page","value":5}));
            cx.notify();
        }))
}
