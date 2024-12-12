// swift-tools-version: 5.8
// The swift-tools-version declares the minimum version of Swift required to build this package.

import PackageDescription

let package = Package(
    name: "SwExt",
    platforms: [
        .macOS("13.0")
    ],
    products: [
        // Products define the executables and libraries a package produces, making them visible to other packages.
        .library(
            name: "SwExt",
            type: .static,
            targets: ["SwExt"]
        )
    ],
    dependencies: [
        .package(url: "https://github.com/trilemma-dev/Blessed.git", from: "0.6.0"),
        .package(url: "https://github.com/trilemma-dev/EmbeddedPropertyList.git", from: "2.0.2"),
        .package(url: "https://github.com/trilemma-dev/SecureXPC.git", from: "0.8.0"),
        .package(url: "https://github.com/sindresorhus/Defaults", from: "7.2.0"),
    ],
    targets: [
        // Targets are the basic building blocks of a package, defining a module or a test suite.
        // Targets can depend on other targets in this package and products from dependencies.
        .target(
            name: "SwExt",
            dependencies: ["CBridge", "Blessed", "EmbeddedPropertyList", "SecureXPC", "Defaults"]
        ),
        .systemLibrary(
            name: "CBridge"),
        .testTarget(
            name: "SwExtTests",
            dependencies: ["SwExt"]
        ),
    ]
)
