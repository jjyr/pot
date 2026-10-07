import AppKit

@MainActor func potIcon(cooking: Bool = false, phase: Double = 0.5, waiting: Bool = false,
                        color: NSColor = .labelColor) -> NSImage
{
    let image = NSImage(size: NSSize(width: 22, height: 22), flipped: false) { _ in
        color.setStroke()
        let bowl = NSBezierPath(); bowl.move(to: NSPoint(x: 4, y: 12)); bowl.line(to: NSPoint(x: 5, y: 6)); bowl.curve(
            to: NSPoint(x: 17, y: 6), controlPoint1: NSPoint(x: 6, y: 1), controlPoint2: NSPoint(x: 16, y: 1)
        ); bowl.line(to: NSPoint(x: 18, y: 12)); bowl.lineWidth = 1.8; bowl.lineCapStyle = .round; bowl.stroke()
        let lid = NSBezierPath()
        lid.move(to: NSPoint(x: 4, y: 15 + (waiting ? 1 : 0)))
        lid.line(to: NSPoint(x: 18, y: 15 - (waiting ? 1 : 0)))
        lid.lineWidth = 1.8; lid.lineCapStyle = .round; lid.stroke()
        if cooking {
            let contents = NSBezierPath()
            contents.move(to: NSPoint(x: 7, y: 9))
            contents.curve(
                to: NSPoint(x: 15, y: 9),
                controlPoint1: NSPoint(x: 9, y: 11 + phase),
                controlPoint2: NSPoint(x: 12, y: 7)
            )
            contents.lineWidth = 1.3; contents.lineCapStyle = .round; contents.stroke()
            color.withAlphaComponent(0.65 + 0.35 * phase).setStroke()
            for x in [8.0, 14.0] {
                let steam = NSBezierPath(); steam.move(to: NSPoint(x: x, y: 17))
                steam.curve(
                    to: NSPoint(x: x + 0.5, y: 21),
                    controlPoint1: NSPoint(x: x - 1, y: 18.5),
                    controlPoint2: NSPoint(x: x + 1.5, y: 19.5)
                )
                steam.lineWidth = 1.1; steam.lineCapStyle = .round; steam.stroke()
            }
        } else if waiting {
            color.setFill(); NSBezierPath(ovalIn: NSRect(x: 10, y: 11, width: 2, height: 2)).fill()
        }
        return true
    }; image.isTemplate = true; return image
}

@MainActor func trayIcon(cooking: Bool, phase: Double, waiting: Bool, failed: Bool, unread: Int,
                         highlighted: Bool) -> NSImage
{
    let image = NSImage(size: NSSize(width: 50, height: 22), flipped: false) { _ in
        let color: NSColor = highlighted ? .white : .labelColor
        let pot = potIcon(cooking: cooking, phase: phase, waiting: waiting, color: color)
        pot.isTemplate = false
        pot.draw(in: NSRect(x: 0, y: 0, width: 22, height: 22))
        if unread > 0 {
            let rect = NSRect(x: 25, y: 4, width: 24, height: 14)
            NSColor.controlAccentColor.withAlphaComponent(highlighted ? 0.4 : 0.16).setFill()
            NSBezierPath(roundedRect: rect, xRadius: 7, yRadius: 7).fill()
            let text = unread > 9 ? "9+" : String(unread)
            let attributes: [NSAttributedString.Key: Any] = [
                .font: NSFont.monospacedDigitSystemFont(ofSize: 10, weight: .medium),
                .foregroundColor: highlighted ? NSColor.white : NSColor.labelColor,
            ]
            let size = (text as NSString).size(withAttributes: attributes)
            (text as NSString).draw(
                at: NSPoint(x: rect.midX - size.width / 2, y: rect.midY - size.height / 2),
                withAttributes: attributes
            )
        }
        if failed {
            color.setFill()
            ("!" as NSString).draw(
                at: NSPoint(x: 18, y: 1),
                withAttributes: [.font: NSFont.systemFont(ofSize: 9, weight: .bold), .foregroundColor: color]
            )
        }
        return true
    }
    image.isTemplate = false
    return image
}

/// The selected Pot Companion character, drawn independently of the monochrome tray.
@MainActor func companionIcon(blink: CGFloat = 1, smile: CGFloat = 0,
                              steam: CGFloat = 0, phase: Double = 0.5) -> NSImage
{
    let image = NSImage(size: NSSize(width: 32, height: 32), flipped: false) { _ in
        let clay = NSColor(srgbRed: 0.80, green: 0.44, blue: 0.31, alpha: 1)
        let ink = NSColor(srgbRed: 0.20, green: 0.15, blue: 0.13, alpha: 1)
        clay.setStroke()
        for x in [3.0, 24.0] {
            let handle = NSBezierPath(ovalIn: NSRect(x: x, y: 11, width: 5, height: 7))
            handle.lineWidth = 2; handle.stroke()
        }
        let body = NSBezierPath()
        body.move(to: NSPoint(x: 6, y: 21))
        body.curve(to: NSPoint(x: 9, y: 5), controlPoint1: NSPoint(x: 5, y: 12),
                   controlPoint2: NSPoint(x: 5, y: 7))
        body.curve(to: NSPoint(x: 23, y: 5), controlPoint1: NSPoint(x: 12, y: 2),
                   controlPoint2: NSPoint(x: 20, y: 2))
        body.curve(to: NSPoint(x: 26, y: 21), controlPoint1: NSPoint(x: 27, y: 7),
                   controlPoint2: NSPoint(x: 27, y: 12))
        body.close()
        clay.setFill(); body.fill()
        NSColor(srgbRed: 0.66, green: 0.32, blue: 0.21, alpha: 1).setFill()
        NSBezierPath(ovalIn: NSRect(x: 6, y: 18, width: 20, height: 6)).fill()
        NSColor(srgbRed: 0.96, green: 0.72, blue: 0.53, alpha: 1).setStroke()
        let rim = NSBezierPath(ovalIn: NSRect(x: 6, y: 18, width: 20, height: 6))
        rim.lineWidth = 1.2; rim.stroke()
        ink.setFill()
        for x in [11.0, 21.0] {
            let height = max(0.5, 2.6 * blink)
            NSBezierPath(ovalIn: NSRect(x: x - 1.3, y: 12.4 - height / 2, width: 2.6, height: height)).fill()
        }
        ink.setStroke()
        let mouth = NSBezierPath(); mouth.move(to: NSPoint(x: 14, y: 9.3))
        mouth.curve(to: NSPoint(x: 18, y: 9.3), controlPoint1: NSPoint(x: 15, y: 7.7 - smile),
                    controlPoint2: NSPoint(x: 17, y: 7.7 - smile))
        mouth.lineWidth = 1.1; mouth.lineCapStyle = .round; mouth.stroke()
        if steam > 0 {
            clay.withAlphaComponent(steam * 0.7).setStroke()
            for (index, x) in [12.0, 20.0].enumerated() {
                let drift = sin(phase * .pi * 2 + Double(index)) * 0.6
                let line = NSBezierPath(); line.move(to: NSPoint(x: x, y: 25))
                line.curve(to: NSPoint(x: x + drift, y: 30.5),
                           controlPoint1: NSPoint(x: x - 1.2 + drift, y: 27),
                           controlPoint2: NSPoint(x: x + 1.2 + drift, y: 28.5))
                line.lineWidth = 1.1; line.lineCapStyle = .round; line.stroke()
            }
        }
        return true
    }
    image.isTemplate = false
    return image
}
