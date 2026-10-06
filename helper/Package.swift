// swift-tools-version: 6.2
import PackageDescription

let package = Package(name: "PotHelper", platforms: [.macOS("26.0")], dependencies: [
    .package(
        url: "https://github.com/apple/containerization.git",
        revision: "bc994b88df46207fad7775b0eabc51947e315881"
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
], swiftLanguageModes: [.v5])
