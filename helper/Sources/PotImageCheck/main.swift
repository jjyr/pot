import Foundation
import Containerization

let args = CommandLine.arguments
if args.count != 4 { fatalError("Usage: PotImageCheck ROOT RESOURCES CHECK_SCRIPT") }
let started = Date()
let root = URL(fileURLWithPath: args[1])
let resources = URL(fileURLWithPath: args[2])
let check = URL(fileURLWithPath: args[3])
try FileManager.default.createDirectory(at: root.appendingPathComponent("data"), withIntermediateDirectories: true)
let store = try ImageStore(path: root.appendingPathComponent("store"))
_ = try await store.load(from: resources.appendingPathComponent("init-image"))
_ = try await store.load(from: resources.appendingPathComponent("sandbox-image"))
let metadata = try JSONSerialization.jsonObject(with: Data(contentsOf: resources.appendingPathComponent("sandbox-image.json"))) as! [String:Any]
var manager = try await ContainerManager(kernel: Kernel(path: resources.appendingPathComponent("vmlinux"), platform: .linuxArm), initfsReference: "ghcr.io/apple/containerization/vminit@sha256:4c1836052eafcbc944c403fb23ded06562962d2402c9dcc651495a32300614df", imageStore: store)
let image = try await store.get(reference: metadata["reference"] as! String)
try? manager.delete("offline-check")
let vm = try await manager.create("offline-check", image: image, rootfsSizeInBytes: 8*1024*1024*1024, networking: false, vm: VMResources(cpus: 4, memoryInBytes: 4*1024*1024*1024)) { config in
 config.process.arguments = ["/bin/sh", "/check/offline-image.sh"]
 config.mounts.append(.share(source: root.appendingPathComponent("data").path, destination: "/data"))
 config.mounts.append(.share(source: check.path, destination: "/check"))
}
do {
 try await vm.create(); try await vm.start()
 let result = try await vm.wait(); try await vm.stop(); try manager.delete("offline-check")
 guard result.exitCode == 0 else { fatalError("Offline check failed; inspect ROOT/data/check.log") }
 print("Offline image startup and shutdown passed in \(Int(Date().timeIntervalSince(started))) seconds (including import/unpack)")
} catch { try? await vm.stop(); try? manager.delete("offline-check"); throw error }
