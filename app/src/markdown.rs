use gpui::{App, SharedString, prelude::*, rgb};
use gpui_kit::base::{
    Theme,
    text::{TextView, TextViewStyle},
};

/// Stable IDs let TextView retain its parser, measured layout and selection across
/// unrelated shell redraws. The component only reparses changed source text.
pub fn render(id: &str, source: &str, user: bool, _dark: bool, cx: &App) -> impl IntoElement {
    let style = TextViewStyle::from_theme(&Theme::global(cx));
    let style = if user {
        style
            .with_foreground(rgb(0xffffff).into())
            .with_link(rgb(0xffffff).into())
    } else {
        style
    };
    TextView::markdown(
        SharedString::from(format!("markdown-{id}")),
        source.to_owned(),
    )
    .style(style)
    .selectable(true)
    .on_link_click(|url, _, _, cx| {
        if url.starts_with("https://") || url.starts_with("http://") {
            cx.open_url(url);
        }
    })
}
