//! Horizontal navigation uses the shared touch recognizer's axis arbitration.
//! Only the left edge owns Back/open; ordinary content keeps scrolling/selection.
use super::*;

#[derive(Clone, Copy, Debug, PartialEq)]
pub(super) enum Destination {
    Open,
    Close,
    Back,
}
#[derive(Clone, Copy, Debug)]
pub(super) struct Swipe {
    destination: Destination,
    distance: f32,
}
impl Swipe {
    fn start(x: f32, dx: f32, dy: f32, menu: bool, child: bool) -> Option<Self> {
        if dx.abs() <= dy.abs() * 1.5 {
            return None;
        }
        let destination = if menu && dx < 0. {
            Destination::Close
        } else if !menu && x <= 24. && dx > 0. {
            if child {
                Destination::Back
            } else {
                Destination::Open
            }
        } else {
            return None;
        };
        Some(Self {
            destination,
            distance: dx,
        })
    }
    fn advance(&mut self, dx: f32, width: f32) {
        self.distance = (self.distance + dx).clamp(-width, width);
    }
    fn fraction(self, width: f32) -> f32 {
        match self.destination {
            Destination::Close => (1. + self.distance / width).clamp(0., 1.),
            _ => (self.distance / width).clamp(0., 1.),
        }
    }
    fn committed(self, width: f32, cancelled: bool) -> bool {
        !cancelled
            && match self.destination {
                Destination::Close => -self.distance >= (width * 0.25).min(100.),
                _ => self.distance >= (width * 0.25).min(100.),
            }
    }
}
impl Shell {
    /// Physical contact boundaries are separate from scroll phases: a new
    /// finger can catch an existing fling without another Scroll Started.
    pub fn touch_started(&mut self) {
        self.navigation_pan_pending = true;
        self.navigation_pan_owned = false;
        self.swipe = None;
    }

    pub(super) fn set_navigation_open(&mut self, open: bool, cx: &mut Context<Self>) {
        self.navigation_open = open;
        self.menu_animation = Some((self.menu_fraction, web_time::Instant::now()));
        cx.notify();
    }
    pub(super) fn animate_menu(&mut self, window: &mut Window, cx: &App) {
        let Some((from, started)) = self.menu_animation else {
            return;
        };
        let t = if cx.reduce_motion() {
            1.
        } else {
            (started.elapsed().as_secs_f32() / 0.18).min(1.)
        };
        let target = if self.navigation_open { 1. } else { 0. };
        self.menu_fraction = from + (target - from) * (1. - (1. - t).powi(3));
        if t < 1. {
            window.request_animation_frame();
        } else {
            self.menu_animation = None;
        }
    }
    pub(super) fn navigation_gestures(&self, cx: &Context<Self>) -> impl IntoElement {
        let entity = cx.entity().downgrade();
        canvas(
            |_, _, _| {},
            move |_, _, window, _| {
                window.on_mouse_event(move |event: &ScrollWheelEvent, phase, window, cx| {
                    if phase != DispatchPhase::Capture {
                        return;
                    }
                    let _ = entity.update(cx, |this, cx| {
                        if !this.narrow {
                            return;
                        }
                        let ScrollDelta::Pixels(delta) = event.delta else {
                            return;
                        };
                        if event.touch_phase == TouchPhase::Started {
                            this.navigation_pan_owned = false;
                            this.navigation_pan_pending = true;
                        }
                        // The recognizer may emit fling frames after Ended. Those
                        // still belong to this pan, even after its menu/route closed.
                        if this.navigation_pan_owned
                            && this.swipe.is_none()
                            && delta.x.abs() >= delta.y.abs()
                        {
                            cx.stop_propagation();
                            window.prevent_default();
                            return;
                        }
                        if this.editor_leave.is_some() {
                            return;
                        }
                        let width = f32::from(window.fully_visible_bounds().size.width);
                        if this.navigation_pan_pending
                            && matches!(event.touch_phase, TouchPhase::Started | TouchPhase::Moved)
                            && (delta.x != px(0.) || delta.y != px(0.))
                        {
                            this.navigation_pan_pending = false;
                            let view = this.client.view.lock().unwrap().clone();
                            let child =
                                !this.editors.is_empty() || this.route(&view).parent().is_some();
                            this.swipe = Swipe::start(
                                f32::from(
                                    event.position.x - window.fully_visible_bounds().origin.x,
                                ),
                                f32::from(delta.x),
                                f32::from(delta.y),
                                this.navigation_open,
                                child,
                            );
                            if let Some(swipe) = this.swipe {
                                this.navigation_pan_owned = true;
                                this.menu_animation = None;
                                if swipe.destination == Destination::Open {
                                    this.navigation_return_focus = window.focused(cx);
                                }
                            }
                        } else if let Some(swipe) = this.swipe.as_mut() {
                            swipe.advance(f32::from(delta.x), width);
                        }
                        let Some(swipe) = this.swipe else {
                            return;
                        };
                        // Capture before scrollable children, after the recognizer chose
                        // a horizontal pan. Long press selection never enters this path.
                        cx.stop_propagation();
                        window.prevent_default();
                        if swipe.destination != Destination::Back {
                            this.menu_fraction = swipe.fraction(width);
                        }
                        if matches!(event.touch_phase, TouchPhase::Ended | TouchPhase::Cancelled) {
                            this.swipe = None;
                            let commit =
                                swipe.committed(width, event.touch_phase == TouchPhase::Cancelled);
                            match swipe.destination {
                                Destination::Back if commit => this
                                    .editor_intents
                                    .push(serde_json::json!({"action":"editor-back"})),
                                Destination::Back => {}
                                Destination::Open => {
                                    this.set_navigation_open(commit, cx);
                                    if commit {
                                        this.navigation_focus.focus(window, cx);
                                    }
                                }
                                Destination::Close if commit => this.close_navigation(window, cx),
                                Destination::Close => this.set_navigation_open(true, cx),
                            }
                        }
                        cx.notify();
                    });
                });
            },
        )
        .absolute()
        .inset_0()
    }
}

#[cfg(test)]
mod tests {
    use super::{Destination, Swipe};
    #[test]
    fn navigation_does_not_steal_content_gestures() {
        assert!(Swipe::start(12., 2., 20., false, false).is_none());
        assert!(Swipe::start(100., 20., 1., false, true).is_none());
        assert!(Swipe::start(12., -20., 1., false, false).is_none());
        assert!(Swipe::start(200., 20., 1., true, false).is_none());
        assert_eq!(
            Swipe::start(12., 20., 1., false, true).unwrap().destination,
            Destination::Back
        );
    }
    #[test]
    fn cancellation_reversal_and_completion() {
        let mut open = Swipe::start(12., 20., 0., false, false).unwrap();
        assert!(!open.committed(393., false));
        open.advance(120., 393.);
        assert!(open.committed(393., false));
        assert!(!open.committed(393., true));
        open.advance(-130., 393.);
        assert!(!open.committed(393., false));
        assert!(open.fraction(393.) < 0.1);
        let mut close = Swipe::start(200., -20., 0., true, false).unwrap();
        close.advance(-110., 393.);
        assert!(close.committed(393., false));
        assert!(close.fraction(393.) < 0.7);
        close.advance(500., 393.);
        assert_eq!(close.fraction(393.), 1.);
        assert!(!close.committed(393., false));
    }
}
