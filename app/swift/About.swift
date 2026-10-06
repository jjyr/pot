import AppKit

enum PotAbout {
    static func show() {
        let credits = NSMutableAttributedString()
        let paragraph = NSMutableParagraphStyle()
        paragraph.alignment = .center
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 12),
            .foregroundColor: NSColor.labelColor,
            .paragraphStyle: paragraph,
        ]
        func line(_ title: String, url: URL? = nil) {
            var style = attributes
            if let url {
                style[.link] = url
            }
            credits.append(NSAttributedString(string: title + "\n", attributes: style))
        }
        if Bundle.main.object(forInfoDictionaryKey: "NSHumanReadableCopyright") == nil {
            line(AppMetadata.copyright)
        }
        line(L.format("about.developer", [AppMetadata.developer]))
        line(L.text("about.project"), url: URL(string: AppMetadata.repository))
        line(L.text("about.license"), url: Bundle.main.url(forResource: "LICENSE", withExtension: "txt"))
        if let licenses = Bundle.main.url(forResource: "RustDependencyLicenses", withExtension: "html") {
            line(L.text("about.rust-licenses"), url: licenses)
        }
        NSApp.activate(ignoringOtherApps: true)
        NSApp.orderFrontStandardAboutPanel(options: [
            .applicationName: "Pot",
            .applicationVersion: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
                ?? AppMetadata.version,
            .version: "",
            .credits: credits,
        ])
    }
}
