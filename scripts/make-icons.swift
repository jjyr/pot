// Asset exporter for the shared Pot Companion vector master.
// Run: mise run icons (links the canonical native vector master).
import AppKit
import Foundation

let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let assets = root.appendingPathComponent("crates/app/assets")
func color(_ value: UInt32, alpha: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((value >> 16) & 255) / 255,
            green: CGFloat((value >> 8) & 255) / 255,
            blue: CGFloat(value & 255) / 255, alpha: alpha)
}

@MainActor func render(size: Int, homeScreen: Bool = false) -> Data {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState(); NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    let transform = NSAffineTransform(); transform.scale(by: CGFloat(size) / 1024); transform.concat()
    let tile = homeScreen ? NSBezierPath(rect: NSRect(x: 0, y: 0, width: 1024, height: 1024)) :
        NSBezierPath(roundedRect: NSRect(x: 48, y: 48, width: 928, height: 928), xRadius: 210, yRadius: 210)
    NSGradient(starting: color(0xF7F4EC), ending: color(0xFFFEFA))!.draw(in: tile, angle: 90)
    companionIcon().draw(in: homeScreen ? NSRect(x: 112, y: 112, width: 800, height: 800) :
        NSRect(x: 76, y: 100, width: 872, height: 872))
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using: .png, properties: [:])!
}

@MainActor func renderMark(size: Int, color: NSColor = .black) -> Data {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    let mark = companionIcon(monochrome: color)
    mark.isTemplate = false
    mark.draw(in: NSRect(x: 0, y: 0, width: size, height: size))
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using: .png, properties: [:])!
}

@MainActor func renderCompanion(size: Int, cooking: Bool) -> Data {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    companionIcon(steam: cooking ? 1 : 0).draw(in: NSRect(x: 0, y: 0, width: size, height: size))
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using: .png, properties: [:])!
}

@main struct IconGenerator {
    @MainActor static func main() throws {
        let uiAssets = root.appendingPathComponent("crates/ui/assets")
        try FileManager.default.createDirectory(at: uiAssets, withIntermediateDirectories: true)
        for cooking in [false, true] {
            try renderCompanion(size: 112, cooking: cooking)
                .write(to: uiAssets.appendingPathComponent(cooking ? "PotCooking.png" : "PotCompanion.png"))
        }
        if CommandLine.arguments.contains("--ui-only") {
            return
        }
        try FileManager.default.createDirectory(at: assets, withIntermediateDirectories: true)
        // README displays at 128 CSS pixels; provide enough detail for 3x screens.
        try render(size: 384).write(to: assets.appendingPathComponent("Pot384.png"))

        if CommandLine.arguments.contains("--readme-only") {
            print("Generated README icon")
            exit(0)
        }

        try render(size: 1024).write(to: assets.appendingPathComponent("Pot.png"))
        try render(size: 128).write(to: assets.appendingPathComponent("Pot128.png"))
        try render(size: 1024, homeScreen: true).write(to: assets.appendingPathComponent("PotHomeScreen.png"))
        for size in [192, 512] {
            try render(size: size, homeScreen: true)
                .write(to: assets.appendingPathComponent("PotHomeScreen-\(size).png"))
        }

        for size in [22, 44] {
            try renderMark(size: size).write(to: assets.appendingPathComponent("PotMenuBar-\(size).png"))
        }
        // The wallpaper is generated from the same native master, with no duplicate character geometry.
        let watermark = renderMark(size: 256, color: .white).base64EncodedString()
        let wallpaper = """
        <svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800" viewBox="0 0 1280 800">
         <defs>
          <linearGradient id="bg" x2="1" y2="1"><stop stop-color="#faf9f6"/><stop offset="1" stop-color="#e9e9e5"/></linearGradient>
          <mask id="companion" mask-type="alpha"><image x="600" y="360" width="80" height="80" href="data:image/png;base64,\(watermark)"/></mask>
         </defs>
         <path fill="url(#bg)" d="M0 0h1280v800H0z"/>
         <path fill="#CF962E" opacity="0.24" mask="url(#companion)" d="M600 360h80v80h-80z"/>
        </svg>
        """
        try wallpaper.write(
            to: root.appendingPathComponent("crates/sandbox-images/linux/default/pot-wallpaper.svg"),
            atomically: true,
            encoding: .utf8
        )

        let iconset = root.appendingPathComponent(".local/d1/Pot.iconset")
        try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
        for size in [16, 32, 128, 256, 512] {
            try render(size: size).write(to: iconset.appendingPathComponent("icon_\(size)x\(size).png"))
            try render(size: size * 2).write(to: iconset.appendingPathComponent("icon_\(size)x\(size)@2x.png"))
        }

        let tool = Process(); tool.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
        tool.arguments = ["-c", "icns", iconset.path, "-o", assets.appendingPathComponent("Pot.icns").path]
        try tool.run(); tool.waitUntilExit(); guard tool.terminationStatus == 0 else { fatalError("iconutil failed") }
        print("Generated Pot Companion application and Home Screen icons")
    }
}
