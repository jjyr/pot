import Containerization
import Darwin
import Foundation

let root = URL(fileURLWithPath: CommandLine.arguments.count > 1 ? CommandLine
    .arguments[1] : NSHomeDirectory() + "/Library/Application Support/pot")
let resources = URL(fileURLWithPath: CommandLine.arguments.count > 2 ? CommandLine.arguments[2] : Bundle.main.bundleURL
    .appendingPathComponent("../Resources/runtime").standardized.path)
do { try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true) }
catch { FileHandle.standardError.write(Data("Runtime directory unavailable: \(error)\n".utf8)); exit(1) }
let ownerFD = open(root.appendingPathComponent("owner.lock").path, O_CREAT | O_RDWR, 0o600)
guard ownerFD >= 0, flock(ownerFD, LOCK_EX | LOCK_NB) == 0 else { fatalError("runtime root already owned") }
let socketPath = root.appendingPathComponent("helper.sock").path
guard socketPath.utf8.count < 104 else { fatalError("Unix socket path too long") }
unlink(socketPath)
signal(SIGPIPE, SIG_IGN)
let server = socket(AF_UNIX, SOCK_STREAM, 0)
var addr = sockaddr_un(); addr.sun_family = sa_family_t(AF_UNIX)
withUnsafeMutableBytes(of: &addr.sun_path) { dest in
    socketPath.utf8CString.withUnsafeBytes { dest.copyBytes(from: $0) }
}

let bound = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(
    server,
    $0,
    socklen_t(MemoryLayout<sockaddr_un>.size)
) } }
guard bound == 0, listen(server, 8) == 0 else { fatalError("cannot listen: \(errno)") }
chmod(socketPath, 0o600)
func event(_ name: String, _ details: [String: Any] = [:]) {
    var row = details; row["event"] = name; row["time"] = ISO8601DateFormatter()
        .string(from: Date()); row["pid"] = getpid()
    if let data = try? JSONSerialization
        .data(withJSONObject: row, options: [.sortedKeys])
    {
        FileHandle.standardOutput.write(data); FileHandle.standardOutput.write(Data([10]))
    }
}

actor Runtime {
    var manager: ContainerManager?; var container: LinuxContainer?; var phase = "stopped"; var generation =
        0; var error: String?; var ip = ""; var task: Task<
            Void,
            Never
        >?
    let id = "pot-local"
    var cpus = 4; var memoryGiB = 8; var diskGiB = 32
    var imageReference = "pot.local/sandbox"
    func loadConfiguration() throws {
        let path = root.appendingPathComponent("sandbox.json")
        guard FileManager.default.fileExists(atPath: path.path) else { return }
        let values = try JSONSerialization.jsonObject(with: Data(contentsOf: path)) as? [String: Int] ?? [:]
        guard let c = values["cpus"], let m = values["memory_gib"], let d = values["disk_gib"], (1 ... 32).contains(c),
              (1 ... 128).contains(m), (4 ... 512).contains(d)
        else { throw NSError(
            domain: "pot",
            code: 4,
            userInfo: [NSLocalizedDescriptionKey: "Invalid Sandbox resource configuration"]
        ) }
        cpus = c; memoryGiB = m; diskGiB = d
    }

    func status() -> [String: Any] {
        let saved = (try? String(contentsOf: root.appendingPathComponent("workspace/url"), encoding: .utf8)) ?? ""
        let token = saved.components(separatedBy: "#").last ?? ""
        return [
            "cpus": cpus,
            "memory_gib": memoryGiB,
            "disk_gib": diskGiB,
            "image": imageReference,
            "phase": phase,
            "generation": generation,
            "error": error ?? "",
            "ip": ip,
            "viewer": ip.isEmpty ? "" : "http://\(ip):17681/#\(token)",
        ]
    }

    func prepare() async throws {
        guard FileManager.default.isReadableFile(atPath: resources.appendingPathComponent("vmlinux").path)
        else { throw NSError(
            domain: "pot",
            code: 1,
            userInfo: [NSLocalizedDescriptionKey: "Missing bundled kernel"]
        ) }
        if manager == nil {
            let metadata = try JSONSerialization
                .jsonObject(with: Data(contentsOf: resources
                        .appendingPathComponent("sandbox-image.json"))) as? [String: Any]
            guard let reference = metadata?["reference"] as? String else { throw NSError(
                domain: "pot",
                code: 5,
                userInfo: [NSLocalizedDescriptionKey: "Bundled Sandbox image missing; run scripts/pot bundle"]
            ) }
            imageReference = reference
            let store = try ImageStore(path: root.appendingPathComponent("store"))
            let initReference = "ghcr.io/apple/containerization/vminit@sha256:4c1836052eafcbc944c403fb23ded06562962d2402c9dcc651495a32300614df"
            if await (try? store.get(reference: initReference)) ==
                nil
            {
                _ = try await store.load(from: resources.appendingPathComponent("init-image"))
            }
            if await (try? store.get(reference: imageReference)) ==
                nil
            {
                _ = try await store.load(from: resources.appendingPathComponent("sandbox-image"))
            }
            manager = try await ContainerManager(
                kernel: Kernel(path: resources.appendingPathComponent("vmlinux"), platform: .linuxArm),
                initfsReference: initReference,
                imageStore: store,
                network: VmnetNetwork()
            )
        }
    }

    func start() -> [String: Any] {
        if phase == "starting" || phase == "running" || phase == "stopping" {
            return status()
        }
        generation += 1; let gen = generation; phase = "starting"; error = nil; ip = ""
        task = Task { await boot(gen) }; event("runtime.starting", status()); return status()
    }

    func boot(_ gen: Int) async {
        do {
            try loadConfiguration(); try await prepare(); try Task.checkCancellation()
            guard gen == generation else { return }
            try? manager?.delete(id)
            let workspace = root.appendingPathComponent("workspace")
            try FileManager.default.createDirectory(at: workspace, withIntermediateDirectories: true)
            try? FileManager.default.removeItem(at: workspace.appendingPathComponent("browser-stopped"))
            var metadata: [String: Any] = [
                "image": imageReference,
                "cpus": cpus,
                "memory_gib": memoryGiB,
                "disk_gib": diskGiB,
            ]
            let image = try await manager!.imageStore.get(reference: imageReference, pull: false)
            if let manifest = try? await image
                .manifest(for: .current)
            {
                metadata["image_bytes"] = manifest.layers.reduce(Int64(0)) { $0 + $1.size }
            }
            try JSONSerialization.data(withJSONObject: metadata).write(
                to: workspace.appendingPathComponent("environment.json"),
                options: .atomic
            )
            var creatingManager = manager!
            let c = try await creatingManager.create(
                id,
                image: image,
                rootfsSizeInBytes: UInt64(diskGiB) * 1024 * 1024 * 1024,
                networking: true,
                vm: VMResources(
                    cpus: cpus,
                    memoryInBytes: UInt64(memoryGiB) * 1024 * 1024 * 1024 + VMResources.guestMemoryOverhead
                )
            ) { config in
                config.cpus = cpus; config.memoryInBytes = UInt64(memoryGiB) * 1024 * 1024 * 1024
                config.process.arguments = ["/bin/sh", "/opt/pot/desktop.sh"]
                config.mounts.append(.share(source: workspace.path, destination: "/data"))
                config.dns = DNS(nameservers: ["1.1.1.1"])
            }
            manager = creatingManager
            container = c
            try Task.checkCancellation(); try await c.create(); try Task.checkCancellation(); try await c.start()
            ip = c.interfaces.first?.ipv4Address.address.description ?? ""
            guard gen == generation else { try? await c.stop(); return }
            phase = "running"; event("runtime.started", status())
            let result = try await c.wait(); event("runtime.guest_exited", ["status": String(describing: result)])
            if gen ==
                generation
            {
                try? await c.stop(); try? manager?.delete(id); container = nil; phase = "stopped"; ip = ""; if result
                    .exitCode !=
                    0
                {
                    phase = "failed"; error = "Guest exited: \(result.exitCode); inspect workspace/desktop.log"
                }
            }
        } catch {
            if let c = container {
                try? await c.stop()
            }; try? manager?.delete(id); container = nil
            if gen == generation {
                phase = "failed"; self.error = String(describing: error); event(
                    "runtime.failed",
                    status()
                )
            }
        }
    }

    func stop() async -> [String: Any] {
        // SIGTERM and parent watchdog can arrive together when the GUI quits.
        // Join the first shutdown instead of tearing down the VM under Browser.close.
        if phase == "stopping" {
            while phase == "stopping" {
                try? await Task.sleep(nanoseconds: 100_000_000)
            }
            return status()
        }
        if phase == "stopped" {
            return status()
        }
        let wasRunning = phase == "running"
        generation += 1; phase = "stopping"
        if !wasRunning {
            task?.cancel()
        }
        if let c = container {
            if wasRunning {
                try? await c.kill(.term)
                for _ in 0 ..< 100 {
                    if FileManager.default
                        .fileExists(atPath: root.appendingPathComponent("workspace/browser-stopped").path)
                    {
                        break
                    }
                    try? await Task.sleep(nanoseconds: 100_000_000)
                }
                event(
                    "browser.shutdown",
                    ["confirmed": FileManager.default
                        .fileExists(atPath: root.appendingPathComponent("workspace/browser-stopped").path)]
                )
            }
            try? await c.stop()
        }
        task?.cancel()
        if let task {
            await task.value
        }
        container = nil; task = nil; try? manager?.delete(id); phase = "stopped"; ip = ""; event(
            "runtime.stopped",
            status()
        ); return status()
    }

    func request(_ object: [String: Any]) async -> [String: Any] {
        let rid = object["id"] as? String ?? UUID().uuidString
        do {
            let value: [String: Any]
            switch object["method"] as? String {
            case "prepare": try await prepare(); value = status()
            case "start": value = start()
            case "status": value = status()
            case "stop": value = await stop()
            case "shutdown": value = await stop(); Task {
                    try? await Task.sleep(nanoseconds: 200_000_000); unlink(socketPath); exit(0)
                }
            default: throw NSError(domain: "pot", code: 2, userInfo: [NSLocalizedDescriptionKey: "Unknown method"])
            }
            return ["id": rid, "result": value]
        } catch { return ["id": rid, "error": String(describing: error)] }
    }
}

let runtime = Runtime()
signal(SIGTERM, SIG_IGN); signal(SIGINT, SIG_IGN)
let sig = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .global()); sig
    .setEventHandler {
        DispatchQueue.global()
            .asyncAfter(deadline: .now() + 15) { unlink(socketPath); exit(1) }; Task {
                _ = await runtime.stop(); unlink(socketPath); exit(0)
            }
    }; sig.resume()
let interrupt = DispatchSource.makeSignalSource(signal: SIGINT, queue: .global()); interrupt
    .setEventHandler { raise(SIGTERM) }; interrupt.resume()
let parent = getppid()
let watchdog = DispatchSource.makeTimerSource(queue: .global()); watchdog
    .schedule(deadline: .now() + 2, repeating: 2); watchdog
    .setEventHandler {
        if getppid() !=
            parent
        {
            DispatchQueue.global()
                .asyncAfter(deadline: .now() + 15) { unlink(socketPath); exit(1) }; Task {
                    _ = await runtime.stop(); unlink(socketPath); exit(0)
                }
        }
    }; watchdog.resume()
event("helper.ready", ["socket": socketPath])
DispatchQueue.global().async {
    while true {
        let fd = accept(server, nil, nil); if fd < 0 {
            continue
        }
        DispatchQueue.global().async {
            defer { close(fd) }
            var timeout = timeval(tv_sec: 10, tv_usec: 0); setsockopt(
                fd,
                SOL_SOCKET,
                SO_RCVTIMEO,
                &timeout,
                socklen_t(MemoryLayout<timeval>.size)
            )
            var data = Data(); var byte: UInt8 = 0
            while data.count < 8192, read(fd, &byte, 1) == 1 {
                if byte == 10 {
                    break
                }; data.append(byte)
            }
            guard let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else { return }
            let done = DispatchSemaphore(value: 0)
            Task {
                let response = await runtime.request(obj)
                if var bytes = try? JSONSerialization
                    .data(withJSONObject: response, options: [.sortedKeys])
                {
                    bytes.append(10); bytes.withUnsafeBytes { _ = Darwin.write(
                        fd,
                        $0.baseAddress,
                        bytes.count
                    ) }
                }
                done.signal()
            }; done.wait()
        }
    }
}

dispatchMain()
