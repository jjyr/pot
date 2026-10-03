use gpui::{div, prelude::*, rgb, px, size, App, Application, Bounds, Context, Window, WindowBounds, WindowOptions};
extern "C" { fn pot_bridge_install(); }
struct Pot;
impl Render for Pot {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div().size_full().flex().flex_col().bg(rgb(0x172027)).text_color(rgb(0xffffff))
            .p_6().gap_4().child("Pot — GPUI integration POC")
            .child("Setup → Chat → Activity / Tasks")
            .child("Native tray + WKWebView/noVNC verification")
    }
}
fn main() {
    Application::new().run(|cx: &mut App| {
        let bounds = Bounds::centered(None, size(px(960.), px(640.)), cx);
        cx.open_window(WindowOptions {window_bounds: Some(WindowBounds::Windowed(bounds)), ..Default::default()}, |window, cx| {
            window.set_window_title("Pot POC");
            cx.new(|_| Pot)
        }).unwrap();
        cx.activate(true);
        unsafe { pot_bridge_install(); }
    });
}
