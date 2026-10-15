//! Raster-only avatars. The same image component handles every avatar source.
use crate::{application::spaces::Directory, gpui::*};
use std::{collections::HashMap, sync::Arc, time::Duration};

#[derive(Default)]
struct Images(HashMap<(u32, u32), (pot_avatar::Avatar, Arc<RenderImage>)>);
impl Global for Images {}
fn image(raster: pot_avatar::Raster) -> Arc<RenderImage> {
    let pixels = image::RgbaImage::from_raw(raster.width, raster.height, raster.bgra).unwrap();
    Arc::new(RenderImage::new(vec![image::Frame::new(pixels)]))
}
fn ensure(key: &str, cx: &mut App) -> (u32, u32) {
    let colors = pot_avatar::colors(key);
    cx.default_global::<Images>()
        .0
        .entry(colors)
        .or_insert_with(|| {
            let avatar = pot_avatar::Avatar::new(colors.0, colors.1);
            let image = image(avatar.raster());
            (avatar, image)
        });
    colors
}
pub fn default_image(key: &str, size: f32, cx: &mut App) -> Div {
    let colors = ensure(key, cx);
    let image = cx.global::<Images>().0[&colors].1.clone();
    div()
        .size(px(size))
        .flex_shrink_0()
        .child(img(image).size_full())
}
struct Reaction {
    reply: String,
    started: web_time::Instant,
    frame: Option<(usize, Arc<RenderImage>)>,
}
#[derive(Default)]
pub struct Animations {
    replies: crate::avatar_state::Replies,
    active: HashMap<String, Reaction>,
    retired: Vec<Arc<RenderImage>>,
}
impl Animations {
    pub fn release_images(&mut self, window: &mut Window, cx: &mut App) {
        // The current window is temporarily absent from App during rendering.
        // Pass it explicitly and release only images painted in previous renders.
        for image in self.retired.drain(..) {
            cx.drop_image(image, Some(window));
        }
    }
    pub fn observe(&mut self, directory: &Directory, visible: bool, cx: &mut App) {
        self.active.retain(|_, reaction| {
            let keep = !cx.reduce_motion()
                && reaction.started.elapsed().as_millis() < pot_avatar::DURATION_MS as u128;
            if !keep && let Some((_, image)) = reaction.frame.take() {
                self.retired.push(image);
            }
            keep
        });
        for space in directory.spaces.values() {
            for assistant in space.assistants["assistants"]
                .as_array()
                .into_iter()
                .flatten()
            {
                let Some(id) = assistant["id"].as_str() else {
                    continue;
                };
                let owner = space.owner(id).key();
                let reply = assistant["reply_id"].as_str().unwrap_or("");
                if !self.replies.observe(
                    &owner,
                    reply,
                    assistant["reply_count"].as_u64().unwrap_or(0),
                    space.connected,
                ) || !visible
                    || cx.reduce_motion()
                {
                    continue;
                }
                if let Some(old) = self.active.insert(
                    owner,
                    Reaction {
                        reply: reply.into(),
                        started: web_time::Instant::now(),
                        frame: None,
                    },
                ) && let Some((_, image)) = old.frame
                {
                    self.retired.push(image);
                }
            }
        }
    }
    pub fn render(&mut self, owner: &str, key: &str, size: f32, cx: &mut App) -> AnyElement {
        let Some(reaction) = self.active.get_mut(owner).filter(|r| {
            !cx.reduce_motion() && r.started.elapsed().as_millis() < pot_avatar::DURATION_MS as u128
        }) else {
            return default_image(key, size, cx).into_any_element();
        };
        let duration = Duration::from_millis(pot_avatar::DURATION_MS);
        // Generate only the current frame, shared by the row and title. Precomputing
        // the whole sequence on arrival stalls the UI, especially in debug builds.
        let progress =
            (reaction.started.elapsed().as_secs_f32() / duration.as_secs_f32()).clamp(0., 1.);
        let index = (progress * 32.).round() as usize;
        if reaction.frame.as_ref().is_none_or(|(old, _)| *old != index) {
            let colors = ensure(key, cx);
            let source = &cx.global::<Images>().0[&colors].0;
            let next = image(source.frame(index as f32 / 32.));
            if let Some((_, old)) = reaction.frame.replace((index, next)) {
                self.retired.push(old);
            }
        }
        let frame = reaction.frame.as_ref().unwrap().1.clone();
        div()
            .size(px(size))
            .relative()
            .flex_shrink_0()
            .child(div().size_full().relative().with_animation(
                SharedString::from(format!("avatar-{owner}-{}", reaction.reply)),
                Animation::new(duration).with_max_fps(60.),
                move |el, _| {
                    el.child(
                        img(frame.clone())
                            .absolute()
                            .left(px(-size * 0.25))
                            .top(px(-size * 0.25))
                            .size(px(size * 1.5)),
                    )
                },
            ))
            .into_any_element()
    }
}
