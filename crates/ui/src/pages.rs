//! Transport-free page components. Host adapters dispatch actions and own permissions.
use crate::{design, localization::Catalog};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Icon, IconName, Sizable,
    button::{Button, ButtonVariants},
    form::{Field, Form},
    input::Input,
};
use serde_json::Value;
use std::rc::Rc;
#[derive(Clone, Copy)]
pub struct Layout {
    pub compact: bool,
}
impl Layout {
    pub const DESKTOP: Self = Self { compact: false };
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
/// Touch menus keep the same 44px target as their invoking controls.
pub fn menu_item(
    label: impl Into<SharedString>,
    cx: &App,
) -> gpui_kit::component::menu::PopupMenuItem {
    use gpui_kit::component::menu::PopupMenuItem;
    let label = label.into();
    if design::touch(cx) {
        PopupMenuItem::element(move |_, _| {
            div()
                .h(px(44.))
                .min_w(px(180.))
                .flex()
                .items_center()
                .text_size(px(16.))
                .child(label.clone())
        })
    } else {
        PopupMenuItem::new(label)
    }
}

pub const PAGE_WIDTH: f32 = 820.;
pub const SIDEBAR: f32 = 56.;
pub const SANDBOX_HEADER: f32 = 56.;

/// Shared density for native and browser surfaces, selected by usable width.
pub trait Control: Sized {
    fn control(self, cx: &App) -> Self;
}
impl Control for Button {
    fn control(self, cx: &App) -> Self {
        if design::touch(cx) {
            self.large().min_w(px(44.)).h(px(44.)).flex_shrink_0()
        } else {
            self.small()
        }
    }
}
impl Control for Input {
    fn control(self, cx: &App) -> Self {
        if design::touch(cx) {
            self.large().h(px(44.)).text_size(px(16.))
        } else {
            self
        }
    }
}
impl Control for Form {
    fn control(self, cx: &App) -> Self {
        if design::touch(cx) {
            self.large()
        } else {
            self.small()
        }
    }
}
pub trait FieldHelp {
    fn help(self, text: impl Into<SharedString>) -> Self;
}
impl FieldHelp for Field {
    fn help(self, text: impl Into<SharedString>) -> Self {
        let text = text.into();
        self.description_fn(move |_, cx| hint(text.clone(), cx))
    }
}

/// Secondary actions precede a full-width primary action on phones.
pub fn editor_actions(secondary: Vec<Button>, primary: Option<Button>, cx: &App) -> Div {
    let mobile = design::mobile(cx);
    div()
        .w_full()
        .min_w_0()
        .flex()
        .justify_end()
        .gap_2()
        .when(mobile, |el| el.flex_col())
        .when(!secondary.is_empty(), |el| {
            el.child(
                div()
                    .flex()
                    .flex_wrap()
                    .justify_end()
                    .gap_2()
                    .min_w_0()
                    .children(secondary.into_iter().map(|button| button.control(cx))),
            )
        })
        .when_some(primary, |el, button| {
            el.child(button.control(cx).when(mobile, |b| b.w_full()))
        })
}

/// Shape against the actual column width: GPUI's intrinsic text measurement can
/// otherwise clip a child before the inherited ellipsis is painted.
pub fn single_line_title(
    text: &str,
    size: f32,
    width: Pixels,
    window: &Window,
    cx: &App,
) -> SharedString {
    let style = TextStyle {
        font_family: cx.theme().font_family.clone(),
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

/// Keep tooltip lifetime tied to its trigger, including native rail navigation.
pub fn action_tooltip(
    id: impl Into<ElementId>,
    label: impl Into<SharedString>,
    button: Button,
) -> Stateful<Div> {
    let label = label.into();
    div()
        .id(id)
        .tooltip(move |window, cx| {
            gpui_kit::component::tooltip::Tooltip::new(label.clone()).build(window, cx)
        })
        .child(button)
}

pub fn page(_layout: Layout, cx: &App) -> Div {
    div()
        .size_full()
        .min_h_0()
        .min_w_0()
        .pl(px(design::sidebar_width(cx)))
        .bg(cx.theme().background)
        .text_color(cx.theme().foreground)
        .text_size(px(if design::mobile(cx) { 16. } else { 14. }))
        .font_weight(FontWeight::NORMAL)
        .line_height(relative(1.5))
        .font_family(cx.theme().font_family.clone())
}
pub fn content(layout: Layout) -> Div {
    div()
        .size_full()
        .min_h_0()
        .min_w_0()
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
        .min_w_0()
        .whitespace_normal()
        .text_size(px(if design::mobile(cx) { 14. } else { 13. }))
        .font_weight(FontWeight::NORMAL)
        .line_height(relative(1.5))
        .text_color(cx.theme().muted_foreground)
        .child(text.into())
}
pub fn card(cx: &App) -> Div {
    div()
        .w_full()
        .p_5()
        .when(design::mobile(cx), |el| el.p_3())
        .rounded(px(10.))
        .border_1()
        .border_color(cx.theme().border)
        .bg(design::surface(cx))
        .flex()
        .flex_col()
        .gap_4()
}

/// Shared editor frame. The Shell supplies its contextual titlebar and Back.
pub fn editor_page(body: impl IntoElement, actions: impl IntoElement, cx: &App) -> Div {
    page(
        Layout {
            compact: design::compact(cx),
        },
        cx,
    )
    .child(
        content(Layout {
            compact: design::compact(cx),
        })
        .max_w(px(760.))
        .child(
            div()
                .flex_1()
                .min_h_0()
                .min_w_0()
                .flex()
                .flex_col()
                .child(body),
        )
        .child(
            div()
                .flex_shrink_0()
                .min_w_0()
                .pt_3()
                .border_t_1()
                .border_color(cx.theme().border)
                .child(actions),
        ),
    )
}

/// Reveal focused controls after focus or the visible viewport changes (keyboard).
#[derive(Clone, Default)]
pub struct InputReveal(std::rc::Rc<std::cell::RefCell<Option<FocusedViewport>>>);
#[derive(PartialEq)]
struct FocusedViewport {
    input: EntityId,
    bounds: Bounds<Pixels>,
}
impl InputReveal {
    pub fn reset(&self) {
        self.0.borrow_mut().take();
    }
}

pub fn reveal_inputs(
    body: impl IntoElement,
    fields: Vec<Entity<gpui_kit::component::input::InputState>>,
    scroll: ScrollHandle,
    reveal: &InputReveal,
) -> Div {
    let reveal = reveal.clone();
    div()
        .size_full()
        .min_h_0()
        .min_w_0()
        .flex()
        .flex_col()
        .child(body)
        .on_children_prepainted(move |_, window, cx| {
            let Some(input) = fields
                .iter()
                .find(|field| field.read(cx).focus_handle(cx).is_focused(window))
            else {
                reveal.reset();
                return;
            };
            let bounds = input.read(cx).input_bounds();
            let viewport = scroll.bounds();
            if bounds.size.height <= px(0.) || viewport.size.height <= px(0.) {
                return;
            }
            let signature = FocusedViewport {
                input: input.entity_id(),
                bounds: viewport,
            };
            if reveal.0.borrow().as_ref() == Some(&signature) {
                return;
            }
            *reveal.0.borrow_mut() = Some(signature);
            // InputState reports the text bounds; include the control's padding
            // and border, especially for 44 px touch inputs.
            let inset = px(if design::mobile(cx) { 16. } else { 8. });
            let delta = if bounds.bottom() + inset > viewport.bottom() {
                bounds.bottom() + inset - viewport.bottom()
            } else if bounds.top() - inset < viewport.top() {
                bounds.top() - inset - viewport.top()
            } else {
                return;
            };
            let scroll = scroll.clone();
            window.on_next_frame(move |window, _| {
                let mut offset = scroll.offset();
                offset.y -= delta;
                scroll.set_offset(offset);
                window.refresh();
            });
        })
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
        Form::horizontal().label_width(px(184.))
    })
    .control(cx)
    .gap_4()
}
pub fn field(label: impl Into<SharedString>) -> Field {
    let label = label.into();
    Field::new().label_fn(move |_, _cx| {
        div()
            .w_full()
            .text_size(px(14.))
            .font_weight(FontWeight::MEDIUM)
            .child(label.clone())
    })
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
        cx.theme().muted_foreground
    };
    div()
        .w_full()
        .p_3()
        .rounded(px(8.))
        .bg(color.opacity(0.07))
        .flex()
        .gap_2()
        .text_size(px(13.))
        .font_weight(FontWeight::NORMAL)
        .line_height(relative(1.5))
        .text_color(color)
        .child(Icon::new(IconName::Info).size(px(15.)))
        .child(
            div()
                .flex_1()
                .min_w_0()
                .whitespace_normal()
                .child(text.into()),
        )
}

pub fn brand_icon(kind: &str) -> Icon {
    let icon = match kind {
        "openai" => Icon::empty().data(include_bytes!("../assets/agents/openai.svg")),
        "anthropic" => Icon::empty().data(include_bytes!("../assets/agents/claude.svg")),
        "gemini" => Icon::empty().data(include_bytes!("../assets/agents/gemini.svg")),
        "deepseek" => Icon::empty().data(include_bytes!("../assets/agents/deepseek.svg")),
        "opencode-go" | "opencode-zen" => {
            Icon::empty().data(include_bytes!("../assets/agents/opencode.svg"))
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
