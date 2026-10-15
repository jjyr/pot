extern crate gpui_kit as gpui;

rust_i18n::i18n!("locales", fallback = "en");
mod artifacts;
mod backend;
mod client;
mod platform;
mod relay;
mod spaces;
use pot_ui::design;
mod i18n;

#[cfg(test)]
mod stream;

use gpui::{prelude::*, *};
use std::ffi::CString;

unsafe extern "C" {
    fn pot_bridge_startup_error(message: *const std::ffi::c_char);
    fn pot_bridge_set_wake(callback: extern "C" fn());
    fn pot_bridge_install();
    fn pot_bridge_hide();
    fn pot_bridge_update(json: *const std::ffi::c_char);
    fn pot_bridge_event(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_ui(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_command(json: *const std::ffi::c_char);
    fn pot_bridge_chinese() -> bool;
    fn pot_bridge_dark() -> bool;
    fn pot_bridge_reduce_motion() -> bool;
}
static UI_WAKE: std::sync::OnceLock<async_channel::Sender<()>> = std::sync::OnceLock::new();
extern "C" fn wake_ui() {
    if let Some(sender) = UI_WAKE.get() {
        let _ = sender.try_send(());
    }
}
use pot_ui::application::Shell;
fn startup_error(error: &str) -> ! {
    eprintln!("Cannot start Pot: {error}");
    if let Ok(message) = CString::new(error) {
        unsafe {
            pot_bridge_startup_error(message.as_ptr());
        }
    }
    std::process::exit(1)
}
fn main() {
    // Configure the embedded backend before GPUI or Tokio starts any threads.
    let root = client::root();
    if let Err(error) = unsafe { pot_server::apply_settings(&root) } {
        startup_error(&error.to_string());
    }
    if let Some(resources) = std::env::current_exe()
        .ok()
        .and_then(|p| {
            p.parent()
                .and_then(|p| p.parent())
                .map(|p| p.join("Resources/runtime"))
        })
        .filter(|p| p.is_dir())
    {
        unsafe {
            std::env::set_var("POT_RUNTIME_RESOURCES", &resources);
            std::env::set_var("POT_BUILTIN_SKILLS_DIR", resources.join("skills"));
            std::env::set_var("POT_MACOS_HELPER", resources.join("PotMacOSHelper"));
            std::env::set_var(
                "POT_CONTAINER_HELPER",
                resources
                    .parent()
                    .unwrap()
                    .parent()
                    .unwrap()
                    .join("Helpers/PotHelper"),
            );
            if std::env::var_os("POT_ACCESS_ASSETS").is_none() {
                std::env::set_var("POT_ACCESS_ASSETS", resources.join("mobile"));
            }
            std::env::set_var(
                "PATH",
                format!(
                    "{}:{}",
                    resources.join("node/bin").display(),
                    std::env::var("PATH").unwrap_or_default()
                ),
            );
        }
    }

    if let Err(error) = backend::ready() {
        startup_error(&error);
    }
    gpui_kit::application()
        .with_assets(pot_ui::application::Assets)
        .run(|cx: &mut App| {
            gpui_kit::init(cx);
            pot_ui::application::platform::install(std::rc::Rc::new(platform::Native::new()));
            crate::i18n::set_locale(if unsafe { pot_bridge_chinese() } {
                "zh-CN"
            } else {
                "en"
            });
            cx.set_global(design::Viewport(px(1080.)));
            design::install(cx);
            gpui_kit::open_window(
                WindowOptions {
                    window_bounds: Some(WindowBounds::Windowed(Bounds::centered(
                        None,
                        size(px(1080.), px(760.)),
                        cx,
                    ))),
                    window_min_size: Some(size(px(800.), px(700.))),
                    titlebar: Some(TitlebarOptions {
                        traffic_light_position: Some(point(px(12.), px(15.))),
                        ..gpui_kit::component::TitleBar::title_bar_options()
                    }),
                    ..gpui_kit::component::TitleBar::window_options()
                },
                cx,
                |window, cx| {
                    window.set_window_title("Pot");
                    window.on_window_should_close(cx, |_, _| {
                        unsafe { pot_bridge_hide() };
                        false
                    });
                    cx.new(|cx: &mut Context<Shell>| {
                        let (sender, receiver) = async_channel::bounded(1);
                        let _ = UI_WAKE.set(sender.clone());
                        unsafe {
                            pot_bridge_set_wake(wake_ui);
                        }
                        let _ = sender.try_send(());
                        cx.spawn(async move |this, cx| {
                            while receiver.recv().await.is_ok() {
                                if this
                                    .update(cx, |this, cx| {
                                        if this.tick(cx) {
                                            cx.notify();
                                        }
                                    })
                                    .is_err()
                                {
                                    break;
                                }
                            }
                        })
                        .detach();
                        Shell::new(window, cx)
                    })
                },
            )
            .unwrap();
            unsafe { pot_bridge_install() };
            cx.activate(true);
        });
}
