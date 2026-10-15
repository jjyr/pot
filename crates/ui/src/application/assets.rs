use gpui::*;
gpui_kit::assets::icon_assets!(
    Icons,
    [
        Cloud,
        Monitor,
        MessageCircle,
        Copy,
        Check,
        ExternalLink,
        Inbox,
        Globe,
        Settings,
        Ellipsis,
        RefreshCw,
        Info,
        ChartPie,
        Plus,
        Close,
        ChevronDown,
        ChevronLeft,
        ChevronRight,
        ArrowUp,
        Square,
        Command,
        ArrowBigUp,
        CornerDownLeft
    ]
);
pub struct Assets;
impl AssetSource for Assets {
    fn load(&self, path: &str) -> gpui::Result<Option<std::borrow::Cow<'static, [u8]>>> {
        if let Some(bytes) = Icons.load(path)? {
            return Ok(Some(bytes));
        }
        #[cfg(not(target_family = "wasm"))]
        {
            gpui_kit::assets::Assets.load(path)
        }
        #[cfg(target_family = "wasm")]
        {
            Ok(None)
        }
    }
    fn list(&self, path: &str) -> gpui::Result<Vec<SharedString>> {
        let mut paths = Icons.list(path)?;
        #[cfg(not(target_family = "wasm"))]
        paths.extend(gpui_kit::assets::Assets.list(path)?);
        paths.sort();
        paths.dedup();
        Ok(paths)
    }
}
