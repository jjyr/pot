// Editable vector master for Pot Companion's application, Home Screen and tray icons.
// Run: swift scripts/make-icons.swift
import AppKit
import Foundation

let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let assets = root.appendingPathComponent("app/assets")
func color(_ value: UInt32, alpha: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((value >> 16) & 255) / 255,
            green: CGFloat((value >> 8) & 255) / 255,
            blue: CGFloat(value & 255) / 255, alpha: alpha)
}

func stroke(_ points: (NSBezierPath) -> Void, width: CGFloat, color: NSColor) {
    let p = NSBezierPath(); points(p); p.lineWidth = width; p.lineCapStyle = .round; p.lineJoinStyle = .round
    color.setStroke(); p.stroke()
}

func companion() {
    // The same 32-point character geometry as the compact native navigation icon.
    let transform = NSAffineTransform(); transform.translateX(by: 64, yBy: 110)
    transform.scale(by: 28); transform.concat()
    for x in [3.0, 24.0] {
        let handle = NSBezierPath(ovalIn: NSRect(x: x - 1, y: 10, width: 7, height: 9))
        handle.appendOval(in: NSRect(x: x + 1, y: 12, width: 3, height: 5))
        handle.windingRule = .evenOdd
        NSGradient(starting: color(0xE69A70), ending: color(0xA34E32))!.draw(in: handle, angle: -60)
    }
    let body = NSBezierPath(); body.move(to: NSPoint(x: 6, y: 21))
    body.curve(to: NSPoint(x: 9, y: 5), controlPoint1: NSPoint(x: 5, y: 12), controlPoint2: NSPoint(x: 5, y: 7))
    body.curve(to: NSPoint(x: 23, y: 5), controlPoint1: NSPoint(x: 12, y: 2), controlPoint2: NSPoint(x: 20, y: 2))
    body.curve(to: NSPoint(x: 26, y: 21), controlPoint1: NSPoint(x: 27, y: 7), controlPoint2: NSPoint(x: 27, y: 12))
    body.close()
    NSGradient(colors: [color(0xAD5437), color(0xCC704D), color(0xE5986B)])!.draw(in: body, angle: 125)
    let opening = NSBezierPath(ovalIn: NSRect(x: 6, y: 18, width: 20, height: 6))
    NSGradient(starting: color(0x77391F), ending: color(0xB8673F))!.draw(in: opening, angle: -80)
    stroke({ p in
        p.move(to: NSPoint(x: 6.2, y: 21))
        p.curve(to: NSPoint(x: 25.8, y: 21), controlPoint1: NSPoint(x: 7, y: 17.4),
                controlPoint2: NSPoint(x: 25, y: 17.4))
    }, width: 0.48, color: color(0xF5C199))
    stroke({ p in
        p.move(to: NSPoint(x: 6.2, y: 21))
        p.curve(to: NSPoint(x: 25.8, y: 21), controlPoint1: NSPoint(x: 7, y: 24.6),
                controlPoint2: NSPoint(x: 25, y: 24.6))
    }, width: 0.25, color: color(0xECA779))
    for x in [11.0, 21.0] {
        let eye = NSBezierPath(ovalIn: NSRect(x: x - 1.1, y: 11.1, width: 2.2, height: 2.6))
        NSGradient(starting: color(0x473327), ending: color(0x251D18))!.draw(in: eye, angle: -60)
    }
    stroke({ p in
        p.move(to: NSPoint(x: 14, y: 9.3))
        p.curve(to: NSPoint(x: 18, y: 9.3), controlPoint1: NSPoint(x: 15, y: 7.7),
                controlPoint2: NSPoint(x: 17, y: 7.7))
    }, width: 0.55, color: color(0x35251C))
}

func render(size: Int, template: Bool = false, homeScreen: Bool = false) -> Data {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState(); NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    let transform = NSAffineTransform(); transform.scale(by: CGFloat(size) / 1024); transform.concat()
    if template {
        // Preserve the tray's existing execution/unread semantics and template silhouette.
        stroke({ p in
            p.move(to: NSPoint(x: 220, y: 555)); p.line(to: NSPoint(x: 270, y: 305))
            p.curve(to: NSPoint(x: 754, y: 305), controlPoint1: NSPoint(x: 320, y: 105),
                    controlPoint2: NSPoint(x: 704, y: 105)); p.line(to: NSPoint(x: 804, y: 555))
        }, width: 82, color: .black)
        stroke({ p in p.move(to: NSPoint(x: 220, y: 705)); p.line(to: NSPoint(x: 804, y: 705)) },
               width: 82, color: .black)
    } else {
        let tile = homeScreen ? NSBezierPath(rect: NSRect(x: 0, y: 0, width: 1024, height: 1024)) :
            NSBezierPath(roundedRect: NSRect(x: 48, y: 48, width: 928, height: 928), xRadius: 210, yRadius: 210)
        NSGradient(starting: color(0xF6E6CF), ending: color(0xFFF8ED))!.draw(in: tile, angle: 90)
        for index in (0 ..< 20).reversed() {
            let spread = CGFloat(index) * 2
            color(0x72402B, alpha: 0.008).setFill()
            NSBezierPath(ovalIn: NSRect(x: 265 - spread, y: 177 - spread / 3,
                                        width: 494 + spread * 2, height: 58 + spread * 2 / 3)).fill()
        }
        companion()
    }
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using: .png, properties: [:])!
}

try FileManager.default.createDirectory(at: assets, withIntermediateDirectories: true)
try render(size: 1024).write(to: assets.appendingPathComponent("Pot.png"))
try render(size: 128).write(to: assets.appendingPathComponent("Pot128.png"))
try render(size: 1024, homeScreen: true).write(to: assets.appendingPathComponent("PotHomeScreen.png"))
try render(size: 44, template: true).write(to: assets.appendingPathComponent("PotTray.png"))
let iconset = root.appendingPathComponent(".local/d1/Pot.iconset")
try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
for size in [16, 32, 128, 256, 512] {
    try render(size: size).write(to: iconset.appendingPathComponent("icon_\(size)x\(size).png"))
    try render(size: size * 2).write(to: iconset.appendingPathComponent("icon_\(size)x\(size)@2x.png"))
}

let tool = Process(); tool.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
tool.arguments = ["-c", "icns", iconset.path, "-o", assets.appendingPathComponent("Pot.icns").path]
try tool.run(); tool.waitUntilExit(); guard tool.terminationStatus == 0 else { fatalError("iconutil failed") }
print("Generated Pot Companion application, Home Screen and tray icons")
