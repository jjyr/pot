// Editable vector master for Pot's application and macOS template status icon.
// Run: swift scripts/make-icons.swift
import AppKit
import Foundation
let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let assets = root.appendingPathComponent("app/assets")
func color(_ value: UInt32) -> NSColor {
    NSColor(srgbRed: CGFloat((value >> 16) & 255)/255, green: CGFloat((value >> 8) & 255)/255, blue: CGFloat(value & 255)/255, alpha: 1)
}
func rounded(_ rect: NSRect, _ radius: CGFloat, _ fill: NSColor) {
    fill.setFill(); NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius).fill()
}
func stroke(_ points: (NSBezierPath)->Void, width: CGFloat, color: NSColor) {
    let p=NSBezierPath(); points(p); p.lineWidth=width; p.lineCapStyle = .round; p.lineJoinStyle = .round; color.setStroke(); p.stroke()
}
func render(size: Int, template: Bool) -> Data {
    let bitmap=NSBitmapImageRep(bitmapDataPlanes:nil,pixelsWide:size,pixelsHigh:size,bitsPerSample:8,samplesPerPixel:4,hasAlpha:true,isPlanar:false,colorSpaceName:.deviceRGB,bytesPerRow:0,bitsPerPixel:0)!
    NSGraphicsContext.saveGraphicsState(); NSGraphicsContext.current=NSGraphicsContext(bitmapImageRep:bitmap)
    let transform=NSAffineTransform(); transform.scale(by:CGFloat(size)/1024); transform.concat()
    if !template {
        let tile=NSBezierPath(roundedRect:NSRect(x:48,y:48,width:928,height:928),xRadius:210,yRadius:210)
        NSGradient(starting:color(0x28534d),ending:color(0x112d2d))!.draw(in:tile,angle: -70)
    }
    let ink=template ? NSColor.black : color(0xfff2d9)
    // Open U-shaped bowl and a floating lid: the native tray animates this lid.
    stroke({p in
        p.move(to:NSPoint(x:220,y:555));p.line(to:NSPoint(x:270,y:305))
        p.curve(to:NSPoint(x:754,y:305),controlPoint1:NSPoint(x:320,y:105),controlPoint2:NSPoint(x:704,y:105))
        p.line(to:NSPoint(x:804,y:555))
    },width:template ? 82:68,color:ink)
    stroke({p in p.move(to:NSPoint(x:220,y:705));p.line(to:NSPoint(x:804,y:705))},width:template ? 82:68,color:ink)
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using:.png,properties:[:])!
}
try FileManager.default.createDirectory(at:assets,withIntermediateDirectories:true)
try render(size:1024,template:false).write(to:assets.appendingPathComponent("Pot.png"))
try render(size:44,template:true).write(to:assets.appendingPathComponent("PotTray.png"))
let iconset=root.appendingPathComponent(".local/d1/Pot.iconset")
try FileManager.default.createDirectory(at:iconset,withIntermediateDirectories:true)
for size in [16,32,128,256,512] {
    try render(size:size,template:false).write(to:iconset.appendingPathComponent("icon_\(size)x\(size).png"))
    try render(size:size*2,template:false).write(to:iconset.appendingPathComponent("icon_\(size)x\(size)@2x.png"))
}
let tool=Process();tool.executableURL=URL(fileURLWithPath:"/usr/bin/iconutil");tool.arguments=["-c","icns",iconset.path,"-o",assets.appendingPathComponent("Pot.icns").path];try tool.run();tool.waitUntilExit();guard tool.terminationStatus==0 else {fatalError("iconutil failed")}
print("Generated Pot.png, PotTray.png and Pot.icns")
