//! Report panics through the browser DOM even when GPUI can no longer render.
use wasm_bindgen::prelude::*;

#[wasm_bindgen]
extern "C" {
    #[wasm_bindgen(catch,js_namespace=window,js_name=potReportPanic)]
    fn report_panic(message: &str) -> Result<(), JsValue>;
}

pub fn install() {
    // GPUI also calls set_once during startup. Consume its once guard before
    // replacing the hook so framework initialization cannot overwrite ours.
    console_error_panic_hook::set_once();
    std::panic::set_hook(Box::new(|info| {
        let _ = report_panic(&info.to_string());
        console_error_panic_hook::hook(info);
    }));
}
