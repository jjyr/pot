// Export retained canonical tray drawing into deterministic Wails frames.
import AppKit
import Foundation

@main struct TrayAtlas {
    @MainActor static func main() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let destination = root.appendingPathComponent("internal/desktop/assets")
        for dark in [false, true] {
            let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 1100, pixelsHigh: 1584,
                                          bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                          colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
            NSAppearance(named: dark ? .darkAqua : .aqua)!.performAsCurrentDrawingAppearance {
                NSGraphicsContext.saveGraphicsState()
                NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
                let scale = NSAffineTransform(); scale.scale(by: 2); scale.concat()
                for state in 0 ..< 4 {
                    for frame in 0 ..< 9 {
                        for unread in 0 ..< 11 {
                            trayIcon(cooking: frame > 0, phase: frame > 0 ? Double(frame - 1) / 7 : 0.5,
                                     waiting: state & 2 != 0, failed: state & 1 != 0, unread: unread,
                                     highlighted: false)
                                .draw(in: NSRect(x: unread * 50, y: 792 - (state * 9 + frame + 1) * 22,
                                                 width: 50, height: 22))
                        }
                    }
                }
                NSGraphicsContext.restoreGraphicsState()
            }
            try bitmap.representation(using: .png, properties: [:])!
                .write(to: destination.appendingPathComponent(dark ? "TrayDark.png" : "TrayLight.png"))
        }
    }
}
