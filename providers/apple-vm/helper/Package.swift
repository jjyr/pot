// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "PotMacOSHelper",
    platforms: [.macOS("26.0")],
    targets: [.executableTarget(name: "PotMacOSHelper")],
    swiftLanguageModes: [.v5]
)
