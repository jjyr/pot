//! Transport-free page components. Host adapters dispatch actions and own permissions.
use crate::{design, localization::Catalog};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Icon, IconName, Sizable,
    button::{Button, ButtonVariants},
    form::{Field, Form},
};
use serde_json::Value;
use std::rc::Rc;
#[derive(Clone, Copy)]
pub struct Layout {
    pub native: bool,
    pub compact: bool,
}
impl Layout {
    pub const NATIVE: Self = Self {
        native: true,
        compact: false,
    };
}
#[derive(Clone, Debug)]
pub struct PageAction {
    pub kind: &'static str,
    pub value: Value,
}
pub type Handler = Rc<dyn Fn(&PageAction, &mut Window, &mut App)>;
pub fn on(
    handler: &Handler,
    kind: &'static str,
    value: Value,
) -> impl Fn(&gpui::ClickEvent, &mut Window, &mut App) + 'static {
    let handler = handler.clone();
    move |_, window, cx| {
        handler(
            &PageAction {
                kind,
                value: value.clone(),
            },
            window,
            cx,
        )
    }
}
pub fn retry(handler: &Handler, catalog: Catalog) -> Button {
    Button::new("retry-refresh")
        .ghost()
        .small()
        .icon(IconName::RefreshCw)
        .label(catalog.text("ui.retry"))
        .on_click(on(handler, "refresh", Value::Null))
}
macro_rules! tr { ($catalog:expr, $key:expr $(,$arg:expr)* $(,)?) => { $catalog.format($key, &[$($arg.to_string()),*]) }; }
pub(crate) use tr;
pub const PAGE_WIDTH: f32 = 820.;
pub const SIDEBAR: f32 = 56.;
pub const SANDBOX_HEADER: f32 = 56.;

/// Shape against the actual column width: GPUI's intrinsic text measurement can
/// otherwise clip a child before the inherited ellipsis is painted.
pub fn single_line_title(
    text: &str,
    size: f32,
    width: Pixels,
    native: bool,
    window: &Window,
    cx: &App,
) -> SharedString {
    let style = TextStyle {
        font_family: if native {
            ".AppleSystemUIFont".into()
        } else {
            cx.theme().font_family.clone()
        },
        font_weight: FontWeight::MEDIUM,
        color: cx.theme().foreground,
        ..Default::default()
    };
    let runs = [style.to_run(text.len())];
    let text: SharedString = text.to_owned().into();
    if window
        .text_system()
        .shape_line(text.clone(), px(size), &runs, None)
        .width
        <= width
    {
        return text;
    }
    use unicode_segmentation::UnicodeSegmentation;
    let boundaries: Vec<usize> = text
        .grapheme_indices(true)
        .map(|(index, _)| index)
        .collect();
    let mut lo = 0;
    let mut hi = boundaries.len();
    while lo < hi {
        let mid = (lo + hi).div_ceil(2);
        let end = boundaries.get(mid).copied().unwrap_or(text.len());
        let candidate: SharedString = format!("{}…", &text[..end]).into();
        let run = style.to_run(candidate.len());
        if window
            .text_system()
            .shape_line(candidate, px(size), &[run], None)
            .width
            <= width
        {
            lo = mid;
        } else {
            hi = mid - 1;
        }
    }
    format!(
        "{}…",
        &text[..boundaries.get(lo).copied().unwrap_or(text.len())]
    )
    .into()
}

pub fn title_tooltip(title: String, window: &mut Window, cx: &mut App) -> AnyView {
    gpui_kit::component::tooltip::Tooltip::element(move |_, _| {
        div().w(px(440.)).whitespace_normal().child(title.clone())
    })
    .max_w(px(480.))
    .build(window, cx)
}

pub fn page(layout: Layout, cx: &App) -> Div {
    div()
        .size_full()
        .pl(px(if layout.native { SIDEBAR } else { 0. }))
        .bg(cx.theme().background)
        .text_color(cx.theme().foreground)
        .text_size(px(14.))
        .font_family(if layout.native {
            ".AppleSystemUIFont"
        } else {
            "IBM Plex Sans"
        })
}
pub fn content(layout: Layout) -> Div {
    div()
        .size_full()
        .max_w(px(PAGE_WIDTH))
        .mx_auto()
        .px_8()
        .when(layout.compact, |el| el.px_4())
        .py_8()
        .when(layout.compact, |el| el.py_4())
        .flex()
        .flex_col()
        .gap_4()
}
pub fn title(text: impl Into<SharedString>) -> Div {
    div()
        .text_size(px(24.))
        .font_weight(FontWeight::SEMIBOLD)
        .child(text.into())
}
pub fn hint(text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .text_size(px(13.))
        .text_color(cx.theme().muted_foreground)
        .child(text.into())
}
pub fn card(cx: &App) -> Div {
    div()
        .w_full()
        .p_5()
        .rounded(px(10.))
        .border_1()
        .border_color(cx.theme().border)
        .bg(design::surface(cx))
        .flex()
        .flex_col()
        .gap_4()
}

/// Quiet grouped choices, with compact native proportions.
pub fn segments(cx: &App) -> Div {
    div()
        .flex()
        .self_start()
        .flex_wrap()
        .items_center()
        .gap_1()
        .p_1()
        .rounded(px(7.))
        .bg(design::navigation(cx))
        .border_1()
        .border_color(cx.theme().border)
}
pub fn form(cx: &App) -> Form {
    (if design::compact(cx) {
        Form::vertical()
    } else {
        Form::horizontal().label_width(px(144.))
    })
    .small()
    .gap_4()
}
pub fn field(label: impl Into<SharedString>) -> Field {
    let label = label.into();
    Field::new().label_fn(move |_, cx| {
        div()
            .w_full()
            .text_size(px(14.))
            .font_weight(FontWeight::MEDIUM)
            .when(!design::compact(cx), |el| el.text_right())
            .child(label.clone())
    })
}
/// Animate a newly opened panel without replaying motion on routine refreshes.
pub fn sheet(panel: Div, key: SharedString, cx: &App) -> AnyElement {
    if cx.reduce_motion() {
        panel.into_any_element()
    } else {
        panel
            .with_animation(
                key,
                Animation::new(std::time::Duration::from_millis(200)),
                |el, t| el.opacity(0.92 + 0.08 * t),
            )
            .into_any_element()
    }
}
pub fn section(text: impl Into<SharedString>) -> Div {
    div()
        .text_size(px(16.))
        .font_weight(FontWeight::SEMIBOLD)
        .child(text.into())
}
pub fn empty(icon: IconName, text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .w_full()
        .py_12()
        .px_6()
        .flex()
        .flex_col()
        .items_center()
        .gap_3()
        .text_color(cx.theme().muted_foreground)
        .text_size(px(13.))
        .child(Icon::new(icon).size(px(24.)))
        .child(text.into())
}
pub fn loading(text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .w_full()
        .py_8()
        .flex()
        .items_center()
        .justify_center()
        .gap_3()
        .text_size(px(13.))
        .text_color(cx.theme().muted_foreground)
        .child(gpui_kit::component::spinner::Spinner::new().small())
        .child(text.into())
}
pub fn notice(text: impl Into<SharedString>, error: bool, cx: &App) -> Div {
    let color = if error {
        cx.theme().danger
    } else {
        design::attention(cx)
    };
    div()
        .w_full()
        .p_3()
        .rounded(px(8.))
        .bg(color.opacity(0.07))
        .flex()
        .gap_2()
        .text_size(px(13.))
        .text_color(color)
        .child(Icon::new(IconName::Info).size(px(15.)))
        .child(text.into())
}

pub fn brand_icon(kind: &str) -> Icon {
    let icon = match kind {
        "openai" => Icon::empty().data(include_bytes!("../../app/assets/agents/openai.svg")),
        "anthropic" => Icon::empty().data(include_bytes!("../../app/assets/agents/claude.svg")),
        "gemini" => Icon::empty().data(include_bytes!("../../app/assets/agents/gemini.svg")),
        "deepseek" => Icon::empty().data(include_bytes!("../../app/assets/agents/deepseek.svg")),
        "opencode-go" | "opencode-zen" => {
            Icon::empty().data(include_bytes!("../../app/assets/agents/opencode.svg"))
        }
        _ => Icon::new(IconName::Globe),
    };
    icon.text_color(gpui::rgb(match kind {
        "openai" => 0x159B7D,
        "anthropic" => 0xD97757,
        "gemini" => 0x4285F4,
        "deepseek" => 0x4D6BFE,
        "opencode-go" => 0x0E9B9B,
        "opencode-zen" => 0x8B5CF6,
        _ => 0x6379A8,
    }))
}
