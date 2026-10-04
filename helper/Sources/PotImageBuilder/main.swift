import Foundation
import Containerization

// Build in a disposable VM, never in the user's live Sandbox.
let args = CommandLine.arguments
if args.count != 4 { fatalError("Usage: PotImageBuilder ROOT RESOURCES INPUT") }
let root = URL(fileURLWithPath: args[1])
let resources = URL(fileURLWithPath: args[2])
let input = URL(fileURLWithPath: args[3])
let cache = root.deletingLastPathComponent().appendingPathComponent("downloads")
try FileManager.default.createDirectory(at: cache, withIntermediateDirectories: true)
let base = "docker.io/library/alpine@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8"
let initRef = "ghcr.io/apple/containerization/vminit@sha256:4c1836052eafcbc944c403fb23ded06562962d2402c9dcc651495a32300614df"
var manager = try await ContainerManager(kernel: Kernel(path: resources.appendingPathComponent("vmlinux"), platform: .linuxArm), initfsReference: initRef, root: root, network: try VmnetNetwork())
try? manager.delete("pot-image-build")
let image = try await manager.imageStore.get(reference: base, pull: true)
let vm = try await manager.create("pot-image-build", image: image, rootfsSizeInBytes: 8*1024*1024*1024, networking: true, vm: VMResources(cpus: 4, memoryInBytes: 4*1024*1024*1024)) { config in
    config.process.arguments = ["/bin/sh", "/build/build.sh"]
    config.mounts.append(.share(source: input.path, destination: "/build"))
    config.mounts.append(.share(source: cache.path, destination: "/cache"))
    config.dns = DNS(nameservers: ["1.1.1.1"])
}
do {
    try await vm.create()
    try await vm.start()
    let result = try await vm.wait()
    try await vm.stop()
    try manager.delete("pot-image-build")
    guard result.exitCode == 0 else { fatalError("Image build failed; see INPUT/build.log") }
    try await manager.imageStore.save(references: [initRef], out: input.appendingPathComponent("init-image"), platform: .current)
} catch {
    try? await vm.stop()
    try? manager.delete("pot-image-build")
    throw error
}
