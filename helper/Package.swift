// swift-tools-version: 6.2
import PackageDescription

let package = Package(name: "PotHelper", platforms: [.macOS("26.0")], dependencies: [
    .package(
        url: "https://github.com/apple/containerization.git",
        revision: "1fca8c55e5eb4c26a0025da68cb8c9f167e6d963"
    ),
], targets: [
    .executableTarget(name: "PotHelper", dependencies: [.product(
        name: "Containerization",
        package: "containerization"
    )]),
    .executableTarget(
        name: "PotImageBuilder",
        dependencies: [.product(name: "Containerization", package: "containerization")]
    ),
    .executableTarget(name: "PotMacOSHelper"),
], swiftLanguageModes: [.v5])
