import AppKit
import Darwin
import Foundation
import Virtualization

/// One owner-enrolled VM per helper. Only fixed private bundle paths are accepted.
let directory = URL(fileURLWithPath: CommandLine.arguments.count == 2 ? CommandLine.arguments[1] : "")
guard CommandLine.arguments.count == 2, directory.path.hasPrefix("/") else { exit(2) }
let generationFile = directory.appendingPathComponent("macos-generation")
let initialGeneration: Int
if FileManager.default.fileExists(atPath: generationFile.path) {
    guard let saved = try? String(contentsOf: generationFile, encoding: .utf8), let value = Int(saved),
          value >= 0 else { exit(4) }
    initialGeneration = value
} else {
    initialGeneration = 0
}

let socketPath = directory.appendingPathComponent("macos-helper.sock").path
guard socketPath.utf8.count < 104 else { exit(2) }
let owner = open(directory.appendingPathComponent("macos-owner.lock").path, O_CREAT | O_RDWR, 0o600)
guard owner >= 0, flock(owner, LOCK_EX | LOCK_NB) == 0 else { exit(3) }
signal(SIGPIPE, SIG_IGN)

final class Machine: NSObject, VZVirtualMachineDelegate {
    var vm: VZVirtualMachine?
    var window: NSWindow?
    var generation = initialGeneration
    func advanceGeneration() throws {
        guard generation < Int.max else { throw NSError(domain: "PotMacOS", code: 7) }
        let next = generation + 1
        let url = directory.appendingPathComponent("macos-generation")
        try Data(String(next).utf8).write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        generation = next
    }

    var failure: String?
    var busy = false
    func status() -> [String: Any] {
        let phase: String = if let vm {
            switch vm.state {
            case .running: "running"
            case .starting, .resuming: "starting"
            case .stopping: "stopping"
            case .stopped: "stopped"
            case .error: "failed"
            default: "unknown"
            }
        } else {
            failure == nil ? "stopped" : "failed"
        }
        return ["phase": phase, "generation": generation, "error": failure ?? "", "protocol": "pot-macos-helper-v1"]
    }

    func prepare() throws {
        guard vm == nil else { return }
        let bundle = directory.appendingPathComponent("macos")
        let configuration = VZVirtualMachineConfiguration()
        let platform = VZMacPlatformConfiguration()
        guard let model =
            try VZMacHardwareModel(dataRepresentation: Data(contentsOf: bundle
                    .appendingPathComponent("hardware-model"))), model.isSupported,
            let identifier =
            try VZMacMachineIdentifier(dataRepresentation: Data(contentsOf: bundle
                    .appendingPathComponent("machine-identifier")))
        else { throw NSError(
            domain: "PotMacOS",
            code: 1,
            userInfo: [NSLocalizedDescriptionKey: "Incompatible original VM platform"]
        ) }
        platform.hardwareModel = model
        platform.machineIdentifier = identifier
        platform
            .auxiliaryStorage = VZMacAuxiliaryStorage(contentsOf: bundle.appendingPathComponent("auxiliary-storage"))
        configuration.platform = platform
        configuration.bootLoader = VZMacOSBootLoader()
        let resourceFile = directory.appendingPathComponent("sandbox.json")
        let settings = (try? JSONSerialization.jsonObject(with: Data(contentsOf: resourceFile))) as? [String: Int] ??
            [:]
        let cpus = settings["cpus"] ?? 4
        let memory = settings["memory_gib"] ?? 8
        guard (2 ... 32).contains(cpus),
              (4 ... 128).contains(memory) else { throw NSError(domain: "PotMacOS", code: 8) }
        configuration.cpuCount = cpus
        configuration.memorySize = UInt64(memory) * 1024 * 1024 * 1024
        let disk = try VZVirtioBlockDeviceConfiguration(attachment: VZDiskImageStorageDeviceAttachment(
            url: bundle.appendingPathComponent("disk.img"),
            readOnly: false
        ))
        configuration.storageDevices = [disk]
        let manifest = try JSONSerialization
            .jsonObject(with: Data(contentsOf: bundle.appendingPathComponent("manifest.json"))) as? [String: Any]
        guard let address = manifest?["mac_address"] as? String,
              let mac = VZMACAddress(string: address)
        else { throw NSError(
            domain: "PotMacOS",
            code: 2
        ) }
        let network = VZVirtioNetworkDeviceConfiguration()
        network.macAddress = mac
        network.attachment = VZNATNetworkDeviceAttachment()
        configuration.networkDevices = [network]
        let filesystem = VZVirtioFileSystemDeviceConfiguration(tag: "PotWorkspace")
        filesystem.share = VZSingleDirectoryShare(directory: VZSharedDirectory(
            url: directory.appendingPathComponent("workspace"),
            readOnly: false
        ))
        configuration.directorySharingDevices = [filesystem]
        let skills = directory.appendingPathComponent("macos-skills")
        if FileManager.default.fileExists(atPath: skills.path) {
            let device = VZVirtioFileSystemDeviceConfiguration(tag: "PotSkills")
            device.share = VZSingleDirectoryShare(directory: VZSharedDirectory(url: skills, readOnly: true))
            configuration.directorySharingDevices.append(device)
        }
        let graphics = VZMacGraphicsDeviceConfiguration()
        graphics.displays = [VZMacGraphicsDisplayConfiguration(
            widthInPixels: 1920,
            heightInPixels: 1200,
            pixelsPerInch: 144
        )]
        configuration.graphicsDevices = [graphics]
        configuration.keyboards = [VZUSBKeyboardConfiguration()]
        configuration.pointingDevices = [VZUSBScreenCoordinatePointingDeviceConfiguration()]
        configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        try configuration.validate()
        vm = VZVirtualMachine(configuration: configuration)
        vm?.delegate = self
        failure = nil
    }

    @MainActor
    func operation(_ method: String) async throws -> [String: Any] {
        if method == "status" {
            return status()
        }
        guard !busy else { throw NSError(
            domain: "PotMacOS",
            code: 3,
            userInfo: [NSLocalizedDescriptionKey: "VM lifecycle busy"]
        ) }
        busy = true
        defer { busy = false }
        switch method {
        case "prepare": try prepare()
        case "start":
            try prepare()
            if let vm, vm.state == .stopped {
                try advanceGeneration()
                try await vm.start()
            }
        case "stop":
            if let vm, vm.state != .stopped {
                // requestStop is only a request. Wait before forced, confirmed stop.
                if vm.canRequestStop {
                    try? vm.requestStop()
                }
                for _ in 0 ..< 100 {
                    if vm.state == .stopped {
                        break
                    }
                    try await Task.sleep(nanoseconds: 100_000_000)
                }
                if vm.state != .stopped {
                    try await vm.stop()
                }
                guard vm.state == .stopped else { throw NSError(
                    domain: "PotMacOS",
                    code: 4,
                    userInfo: [NSLocalizedDescriptionKey: "VM stop unconfirmed"]
                ) }
                try advanceGeneration()
            }
            window?.close(); window = nil
        case "display":
            guard let vm, vm.state == .running else { throw NSError(domain: "PotMacOS", code: 5) }
            if window == nil {
                let view = VZVirtualMachineView()
                view.virtualMachine = vm
                view.capturesSystemKeys = true
                let panel = NSWindow(
                    contentRect: NSRect(x: 0, y: 0, width: 960, height: 600),
                    styleMask: [.titled, .closable, .resizable],
                    backing: .buffered,
                    defer: false
                )
                panel.title = "Pot macOS — \(directory.lastPathComponent)"
                panel.contentView = view
                panel.isReleasedWhenClosed = false
                window = panel
            }
            window?.makeKeyAndOrderFront(nil)
            NSApplication.shared.activate(ignoringOtherApps: true)
        default: throw NSError(
                domain: "PotMacOS",
                code: 6,
                userInfo: [NSLocalizedDescriptionKey: "Unsupported lifecycle operation"]
            )
        }
        return status()
    }

    func virtualMachine(_: VZVirtualMachine, didStopWithError error: Error) {
        failure = String(describing: error)
    }
}

@Sendable func writeReply(_ descriptor: Int32, _ value: [String: Any]) {
    guard var data = try? JSONSerialization.data(withJSONObject: value) else { return }
    data.append(10)
    data.withUnsafeBytes { buffer in
        var offset = 0
        while offset < buffer.count {
            let count = write(descriptor, buffer.baseAddress!.advanced(by: offset), buffer.count - offset)
            if count <= 0 {
                break
            }
            offset += count
        }
    }
}

let listener = socket(AF_UNIX, SOCK_STREAM, 0)
var address = sockaddr_un()
address.sun_family = sa_family_t(AF_UNIX)
withUnsafeMutableBytes(of: &address.sun_path) { destination in
    socketPath.utf8CString.withUnsafeBytes { destination.copyBytes(from: $0) }
}

guard !FileManager.default.fileExists(atPath: socketPath) else { exit(3) }
let bound = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(
    listener,
    $0,
    socklen_t(MemoryLayout<sockaddr_un>.size)
) } }
guard bound == 0, listen(listener, 8) == 0 else { exit(3) }
chmod(socketPath, 0o600)
let application = NSApplication.shared
application.setActivationPolicy(.accessory)
let machine = Machine()
DispatchQueue.global().async {
    while true {
        let client = accept(listener, nil, nil)
        if client < 0 {
            continue
        }
        var uid: uid_t = 0; var gid: gid_t = 0
        guard getpeereid(client, &uid, &gid) == 0, uid == getuid() else { close(client); continue }
        var timeout = timeval(tv_sec: 5, tv_usec: 0)
        setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(client, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        var bytes = [UInt8]()
        var byte: UInt8 = 0
        while bytes.count <= 65536, read(client, &byte, 1) == 1, byte != 10 {
            bytes.append(byte)
        }
        guard byte == 10, bytes.count <= 65536,
              let request = try? JSONSerialization.jsonObject(with: Data(bytes)) as? [String: Any],
              request["version"] as? Int == 1, let id = request["id"] as? String, id.utf8.count <= 64,
              let method = request["method"] as? String
        else { close(client); continue }
        Task { @MainActor in
            do { try await writeReply(client, ["version": 1, "id": id, "result": machine.operation(method)]) }
            catch { writeReply(
                client,
                ["version": 1, "id": id, "error": String(describing: error).prefix(1024).description]
            ) }
            close(client)
        }
    }
}

application.run()
