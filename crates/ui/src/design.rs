//! Approved Pot tokens, shared by all Kit surfaces and controls.
use crate::gpui;
use gpui::{App, Global, Hsla, Pixels, px, rgb};
use gpui_kit::component::Theme;
use std::time::Duration;

pub struct Viewport(pub Pixels);
impl Global for Viewport {}
pub fn compact(cx: &App) -> bool {
    cx.global::<Viewport>().0 < px(900.)
}
pub fn color(light: u32, dark: u32, cx: &App) -> Hsla {
    rgb(if Theme::global(cx).is_dark() {
        dark
    } else {
        light
    })
    .into()
}
pub fn surface(cx: &App) -> Hsla {
    color(0xffffff, 0x2b2b2b, cx)
}
pub fn navigation(cx: &App) -> Hsla {
    color(0xf4f4f4, 0x282828, cx)
}
pub fn attention(cx: &App) -> Hsla {
    color(0x885b13, 0xd7af66, cx)
}

pub fn install(cx: &mut App) {
    Theme::update(cx, |theme| {
        let dark = theme.is_dark();
        let c = |light, night| rgb(if dark { night } else { light }).into();
        let colors = &mut theme.colors;
        colors.background = c(0xfdfdfd, 0x202020);
        colors.foreground = c(0x262626, 0xf2f2f2);
        colors.muted_foreground = c(0x646464, 0xb7b7b7);
        colors.border = c(0xe8e8e8, 0x414141);
        colors.input = c(0xd6d6d6, 0x555555);
        colors.popover = c(0xffffff, 0x2b2b2b);
        colors.popover_foreground = colors.foreground;
        colors.group_box = colors.popover;
        colors.group_box_foreground = colors.foreground;
        colors.accordion = colors.popover;
        colors.muted = c(0xf4f4f4, 0x282828);
        colors.secondary = c(0xeceff3, 0x414141);
        colors.secondary_foreground = colors.foreground;
        colors.secondary_hover = c(0xe4e8ed, 0x4a4a4a);
        colors.secondary_active = c(0xdce1e8, 0x505050);
        colors.accent = colors.secondary;
        colors.accent_foreground = colors.foreground;
        colors.primary = c(0x0068da, 0x0a84ff);
        colors.primary_foreground = rgb(0xffffff).into();
        colors.primary_hover = c(0x005ec6, 0x2692ff);
        colors.primary_active = c(0x0054b0, 0x0073e6);
        colors.ring = colors.primary.opacity(0.65);
        colors.link = colors.primary;
        colors.link_hover = colors.primary_hover;
        colors.link_active = colors.primary_active;
        colors.blue = colors.primary;
        colors.blue_light = c(0xe7f2ff, 0x243a52);
        colors.button = colors.popover;
        colors.button_foreground = colors.foreground;
        colors.button_hover = c(0xebeff5, 0x383e47);
        colors.button_active = c(0xdde5ef, 0x444e5c);
        colors.button_primary = colors.primary;
        colors.button_primary_foreground = colors.primary_foreground;
        colors.button_primary_hover = colors.primary_hover;
        colors.button_primary_active = colors.primary_active;
        colors.button_secondary = colors.secondary;
        colors.button_secondary_foreground = colors.foreground;
        colors.button_secondary_hover = colors.secondary_hover;
        colors.button_secondary_active = colors.secondary_active;
        colors.list = colors.popover;
        colors.list_hover = c(0xf0f3f7, 0x343a43);
        colors.list_active = colors.secondary;
        colors.list_active_border = colors.border;
        colors.list_head = colors.muted;
        colors.selection = c(0xdcecff, 0x315375);
        colors.sidebar = colors.muted;
        colors.sidebar_foreground = colors.foreground;
        colors.sidebar_border = colors.border;
        colors.title_bar = colors.muted;
        colors.title_bar_border = colors.border;
        colors.tab_bar = colors.muted;
        colors.tab_bar_segmented = colors.muted;
        colors.tab = colors.muted;
        colors.tab_active = colors.popover;
        colors.tab_foreground = colors.muted_foreground;
        colors.tab_active_foreground = colors.foreground;
        colors.success = c(0x247844, 0x78be8a);
        colors.warning = c(0x885b13, 0xd7af66);
        colors.danger = c(0xbd342d, 0xff6961);
        colors.button_danger = colors.danger;
        colors.chart_1 = c(0x4b82c4, 0x82b9f6);
        colors.chart_2 = c(0x68a090, 0x8dc3b1);
        colors.chart_3 = c(0xc29b59, 0xd7af66);
        colors.chart_4 = c(0x9187b5, 0xb2a6d6);
        colors.chart_5 = c(0xb88a8a, 0xdca0a0);
        colors.chart_grid = colors.border;
        colors.progress_bar = colors.chart_1;
        colors.scrollbar_thumb = colors.muted_foreground.opacity(0.3);
        colors.scrollbar_thumb_hover = colors.muted_foreground.opacity(0.5);
        theme.radius = px(6.);
        theme.radius_lg = px(12.);
        theme.font_family = ".AppleSystemUIFont".into();
        theme.font_size = px(14.);
        theme.motion.duration_fast = Duration::from_millis(90);
        theme.motion.duration_normal = Duration::from_millis(140);
        theme.motion.duration_slow = Duration::from_millis(220);
    });
}
