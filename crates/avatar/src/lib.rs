//! Theme-independent Pot avatar images and their single-rebound motion.
use sha2::{Digest, Sha256};
use tiny_skia::{
    ColorU8, FillRule, FilterQuality, Paint, PathBuilder, Pixmap, PixmapPaint, Transform,
};

pub const SIZE: u32 = 128;
// 3x resolution for the largest animated slot (32 px), plus transform padding.
pub const FRAME_SIZE: u32 = 144;
pub const DURATION_MS: u64 = 650;
pub const FOREGROUNDS: [u32; 8] = [
    0x276451, 0x904e39, 0x685083, 0x3a6590, 0x8a6928, 0x8b4a68, 0x40546c, 0x5a6f3c,
];
pub const BACKGROUNDS: [u32; 8] = [
    0xdce9e2, 0xf5ddd2, 0xe5ddf1, 0xdce8f4, 0xf4e8c8, 0xf1dce6, 0xdde4ec, 0xe3e8d9,
];
/// The durable random creation identity independently selects both palette indices.
pub fn colors(request: &str) -> (u32, u32) {
    let hash = Sha256::digest(request.as_bytes());
    (
        FOREGROUNDS[hash[0] as usize % 8],
        BACKGROUNDS[hash[1] as usize % 8],
    )
}
pub struct Raster {
    pub width: u32,
    pub height: u32,
    /// Straight-alpha BGRA, matching the shared UI renderer's image boundary.
    pub bgra: Vec<u8>,
}
fn raster(pixmap: &Pixmap) -> Raster {
    Raster {
        width: pixmap.width(),
        height: pixmap.height(),
        bgra: pixmap
            .pixels()
            .iter()
            .flat_map(|p| {
                let p = p.demultiply();
                [p.blue(), p.green(), p.red(), p.alpha()]
            })
            .collect(),
    }
}
pub struct Avatar(Pixmap);
impl Avatar {
    pub fn new(foreground: u32, background: u32) -> Self {
        let mut image = Pixmap::new(SIZE, SIZE).unwrap();
        let mut paint = Paint::default();
        paint.set_color_rgba8(
            (background >> 16) as u8,
            (background >> 8) as u8,
            background as u8,
            255,
        );
        let s = SIZE as f32;
        let r = s * 0.232;
        let mut path = PathBuilder::new();
        path.move_to(r, 0.);
        path.line_to(s - r, 0.);
        path.quad_to(s, 0., s, r);
        path.line_to(s, s - r);
        path.quad_to(s, s, s - r, s);
        path.line_to(r, s);
        path.quad_to(0., s, 0., s - r);
        path.line_to(0., r);
        path.quad_to(0., 0., r, 0.);
        path.close();
        image.fill_path(
            &path.finish().unwrap(),
            &paint,
            FillRule::Winding,
            Transform::identity(),
            None,
        );
        let mut mark = Pixmap::decode_png(include_bytes!("../assets/PotMask.png")).unwrap();
        for pixel in mark.pixels_mut() {
            *pixel = ColorU8::from_rgba(
                (foreground >> 16) as u8,
                (foreground >> 8) as u8,
                foreground as u8,
                pixel.alpha(),
            )
            .premultiply();
        }
        image.draw_pixmap(
            0,
            0,
            mark.as_ref(),
            &PixmapPaint {
                quality: FilterQuality::Bicubic,
                ..Default::default()
            },
            Transform::from_row(
                s * 0.92 / mark.width() as f32,
                0.,
                0.,
                s * 0.92 / mark.height() as f32,
                s * 0.04,
                s * 0.04,
            ),
            None,
        );
        Self(image)
    }
    pub fn raster(&self) -> Raster {
        raster(&self.0)
    }
    pub fn png(&self) -> Vec<u8> {
        self.0.encode_png().unwrap()
    }
    /// Transform the whole raster image, including its background, around its center.
    pub fn frame(&self, progress: f32) -> Raster {
        let (scale, angle) = motion(progress);
        let scale = scale * FRAME_SIZE as f32 / (SIZE as f32 * 1.5);
        let radians = angle.to_radians();
        let a = scale * radians.cos();
        let b = scale * radians.sin();
        let center = FRAME_SIZE as f32 / 2.;
        let half = SIZE as f32 / 2.;
        let mut frame = Pixmap::new(FRAME_SIZE, FRAME_SIZE).unwrap();
        frame.draw_pixmap(
            0,
            0,
            self.0.as_ref(),
            &PixmapPaint {
                quality: FilterQuality::Bicubic,
                ..Default::default()
            },
            Transform::from_row(
                a,
                b,
                -b,
                a,
                center - half * a + half * b,
                center - half * b - half * a,
            ),
            None,
        );
        raster(&frame)
    }
}
/// Match the approved cubic-bezier timing: slow build, fast contraction, short settle.
fn bezier(t: f32, x1: f32, y1: f32, x2: f32, y2: f32) -> f32 {
    let sample = |u: f32, a: f32, b: f32| {
        3. * (1. - u).powi(2) * u * a + 3. * (1. - u) * u * u * b + u * u * u
    };
    let (mut lo, mut hi) = (0., 1.);
    for _ in 0..18 {
        let mid = (lo + hi) / 2.;
        if sample(mid, x1, x2) < t {
            lo = mid;
        } else {
            hi = mid;
        }
    }
    sample((lo + hi) / 2., y1, y2)
}
pub fn motion(progress: f32) -> (f32, f32) {
    if progress <= 0. || progress >= 1. {
        return (1., 0.);
    }
    let (start, end, u, angle_start, angle_end) = if progress < 0.60 {
        (
            1.,
            1.16,
            bezier(progress / 0.60, 0.55, 0., 0.85, 0.4),
            0.,
            -6.,
        )
    } else if progress < 0.84 {
        (
            1.16,
            0.94,
            bezier((progress - 0.60) / 0.24, 0.25, 0.1, 0.25, 1.),
            -6.,
            3.,
        )
    } else {
        (
            0.94,
            1.,
            bezier((progress - 0.84) / 0.16, 0.2, 0.8, 0.3, 1.),
            3.,
            0.,
        )
    };
    (
        start + (end - start) * u,
        angle_start + (angle_end - angle_start) * u,
    )
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn all_color_pairs_are_selected_independently_and_images_are_stable() {
        let pairs: std::collections::BTreeSet<_> = (0..4096)
            .map(|i| colors(&format!("creation-{i}")))
            .collect();
        assert_eq!(pairs.len(), 64);
        let (foreground, background) = colors("creation-1");
        let image = Avatar::new(foreground, background);
        assert_eq!(image.png(), Avatar::new(foreground, background).png());
        let pixels = Pixmap::decode_png(&image.png()).unwrap();
        assert_eq!((pixels.width(), pixels.height()), (SIZE, SIZE));
        assert_eq!(pixels.pixel(0, 0).unwrap().alpha(), 0);
        assert_eq!(
            pixels.pixel(64, 8).unwrap().demultiply(),
            ColorU8::from_rgba(
                (background >> 16) as u8,
                (background >> 8) as u8,
                background as u8,
                255
            )
        );
        assert!(pixels.pixels().iter().any(|p| p.demultiply()
            == ColorU8::from_rgba(
                (foreground >> 16) as u8,
                (foreground >> 8) as u8,
                foreground as u8,
                255
            )));
    }
    #[test]
    fn rebound_has_one_expansion_and_one_contraction_and_includes_background() {
        assert_eq!(motion(0.), (1., 0.));
        assert_eq!(motion(1.), (1., 0.));
        assert!((motion(0.6).0 - 1.16).abs() < 0.0001);
        assert!((motion(0.84).0 - 0.94).abs() < 0.0001);
        assert!(motion(0.3).0 < 1.08);
        let avatar = Avatar::new(FOREGROUNDS[0], BACKGROUNDS[0]);
        let still = avatar.frame(0.);
        let expanded = avatar.frame(0.6);
        assert_eq!(still.width, FRAME_SIZE);
        assert_ne!(still.bgra, expanded.bgra);
        assert_eq!(still.bgra, avatar.frame(1.).bgra);
    }
}
