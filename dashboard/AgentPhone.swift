import SwiftUI
import AppKit
import ImageIO
import Darwin

struct Envelope: Decodable { let ok: Bool; let result: Fleet?; let error: Failure? }
struct Failure: Decodable { let message: String }
struct Fleet: Decodable {
    let phones: [Phone]
    let physicalMemoryBytes: Int64
    let budgetBytes: Int64
    let simulatorMemoryBytes: Int64
    let dataDirectory: String
}
struct Phone: Decodable, Identifiable {
    var id: String { udid }
    let udid, name, state: String
    let managed: Bool
    let memoryBytes: Int64?
    let owner: String?
    let busy: Bool?
    let observation: Observation?
}
struct Observation: Decodable {
    let id, app, capturedAt: String
    let durationMs: Int
    let screenshot: String?
    let elements: [Element]
}
struct Element: Decodable { let label: String; let actionable: Bool }

struct VisionRecord: Decodable {
    struct Frame: Decodable {
        struct Cursor: Decodable, Equatable { let epoch: String; let sequence: UInt64 }
        let device: String
        let cursor: Cursor
        let capturedAt: String
        let width, height: Int
        let jpeg: Data?
    }
    let device, state: String
    let frame: Frame?
    let error: String?
}

struct VisionDelivery {
    let state: String
    let image: NSImage?
    let capturedAt: Date?
    let detail: String?
}

final class VisionMailbox: @unchecked Sendable {
    private let lock = NSLock()
    private var latest: VisionDelivery?
    private var scheduled = false
    private let deliver: @MainActor (VisionDelivery) -> Void
    init(deliver: @escaping @MainActor (VisionDelivery) -> Void) { self.deliver = deliver }
    func offer(_ value: VisionDelivery) {
        lock.lock()
        latest = value
        let enqueue = !scheduled
        scheduled = true
        lock.unlock()
        if enqueue {
            DispatchQueue.main.async { [self] in
                lock.lock()
                let value = latest
                latest = nil
                scheduled = false
                lock.unlock()
                if let value { deliver(value) }
            }
        }
    }
}

final class VisionReader: @unchecked Sendable {
    private let lock = NSLock()
    private var stopped = false
    private var process: Process?
    private let wake = DispatchSemaphore(value: 0)
    private let mailbox: VisionMailbox
    init(mailbox: VisionMailbox) { self.mailbox = mailbox }
    func start(cli: String, device: String) {
        DispatchQueue.global(qos: .utility).async { [self] in run(cli: cli, device: device) }
    }
    func stop() {
        lock.lock()
        stopped = true
        let current = process
        lock.unlock()
        wake.signal()
        terminate(current)
    }
    private func terminate(_ current: Process?) {
        guard let current, current.isRunning else { return }
        current.terminate()
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 0.8) {
            if current.isRunning { kill(current.processIdentifier, SIGKILL) }
        }
    }
    private var isStopped: Bool {
        lock.lock()
        defer { lock.unlock() }
        return stopped
    }
    private func run(cli: String, device: String) {
        var retry = 0.25
        let dates = ISO8601DateFormatter()
        dates.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        while !isStopped {
            let child = Process(), pipe = Pipe()
            child.executableURL = URL(fileURLWithPath: cli)
            child.arguments = ["watch", "--device", device]
            child.standardOutput = pipe
            child.standardError = FileHandle.nullDevice
            child.standardInput = FileHandle.nullDevice
            mailbox.offer(VisionDelivery(state: "reconnecting", image: nil, capturedAt: nil, detail: nil))
            do {
                lock.lock()
                if stopped { lock.unlock(); return }
                process = child
                do { try child.run() } catch { process = nil; lock.unlock(); throw error }
                lock.unlock()
                var pending = Data()
                var lastCursor: VisionRecord.Frame.Cursor?
                var lastImage: NSImage?
                var lastDate: Date?
                while !isStopped {
                    let chunk = try pipe.fileHandleForReading.read(upToCount: 64 * 1024) ?? Data()
                    if chunk.isEmpty { break }
                    pending.append(chunk)
                    while let newline = pending.firstIndex(of: 10) {
                        let line = Data(pending[..<newline])
                        pending.removeSubrange(...newline)
                        guard line.count <= 3 * 1024 * 1024 else { throw CocoaError(.fileReadTooLarge) }
                        let record = try JSONDecoder().decode(VisionRecord.self, from: line)
                        guard record.device == device else { throw CocoaError(.fileReadCorruptFile) }
                        if let frame = record.frame, frame.cursor != lastCursor {
                            guard frame.device == device,
                                  frame.width == 402, frame.height > 0, frame.height <= 8192,
                                  let date = dates.date(from: frame.capturedAt),
                                  let jpeg = frame.jpeg, jpeg.count <= 2 * 1024 * 1024,
                                  let source = CGImageSourceCreateWithData(jpeg as CFData, nil),
                                  let encoded = CGImageSourceCreateImageAtIndex(source, 0, [kCGImageSourceShouldCacheImmediately: true] as CFDictionary),
                                  encoded.width == frame.width, encoded.height == frame.height,
                                  let bitmap = CGContext(data: nil, width: encoded.width, height: encoded.height, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
                            else { throw CocoaError(.fileReadCorruptFile) }
                            bitmap.draw(encoded, in: CGRect(x: 0, y: 0, width: encoded.width, height: encoded.height))
                            guard let decoded = bitmap.makeImage() else { throw CocoaError(.fileReadCorruptFile) }
                            lastImage = NSImage(cgImage: decoded, size: NSSize(width: decoded.width, height: decoded.height))
                            lastCursor = frame.cursor
                            lastDate = date
                            retry = 0.25
                        }
                        mailbox.offer(VisionDelivery(state: record.state, image: lastImage, capturedAt: lastDate, detail: record.error))
                    }
                    guard pending.count <= 3 * 1024 * 1024 else { throw CocoaError(.fileReadTooLarge) }
                }
            } catch {
                mailbox.offer(VisionDelivery(state: "reconnecting", image: nil, capturedAt: nil, detail: error.localizedDescription))
            }
            terminate(child)
            if child.processIdentifier != 0 { child.waitUntilExit() }
            try? pipe.fileHandleForReading.close()
            lock.lock()
            process = nil
            lock.unlock()
            if isStopped { return }
            mailbox.offer(VisionDelivery(state: "reconnecting", image: nil, capturedAt: nil, detail: "Waiting for the local vision service"))
            _ = wake.wait(timeout: .now() + retry)
            retry = min(retry * 2, 4)
        }
    }
}

@MainActor final class VisionModel: ObservableObject {
    @Published var image: NSImage?
    @Published var state = "offline"
    @Published var capturedAt: Date?
    @Published var detail: String?
    private var reader: VisionReader?
    private var generation = UUID()
    func start(cli: String, device: String) {
        guard reader == nil else { return }
        generation = UUID()
        let expected = generation
        state = "reconnecting"
        let mailbox = VisionMailbox { [weak self] value in
            guard let self, self.generation == expected else { return }
            if let image = value.image { self.image = image }
            if let date = value.capturedAt { self.capturedAt = date }
            self.state = value.state
            self.detail = value.detail
        }
        let reader = VisionReader(mailbox: mailbox)
        self.reader = reader
        reader.start(cli: cli, device: device)
    }
    func stop() {
        generation = UUID()
        reader?.stop()
        reader = nil
        state = "offline"
    }
    func label(at now: Date) -> String {
        if state == "offline" { return "Offline" }
        if state == "live", let capturedAt, now.timeIntervalSince(capturedAt) < 3 { return "Live" }
        if state == "live" || state == "stale" { return "Stale" }
        return "Reconnecting"
    }
}

@MainActor final class FleetModel: ObservableObject {
    @Published var fleet: Fleet?
    @Published var error: String?
    @Published var updating = false
    let cli = Bundle.main.object(forInfoDictionaryKey: "AgentPhoneCLIPath") as? String ?? ""
    func refresh() async {
        guard !updating else { return }
        updating = true
        defer { updating = false }
        let executable = cli
        do {
            let data: Data = try await Task.detached {
                let process = Process(), output = Pipe()
                process.executableURL = URL(fileURLWithPath: executable)
                process.arguments = ["list"]
                process.standardOutput = output
                process.standardError = FileHandle.nullDevice
                try process.run()
                let data = output.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                return data
            }.value
            let reply = try JSONDecoder().decode(Envelope.self, from: data)
            guard reply.ok, let value = reply.result else {
                error = reply.error?.message ?? "Unable to read fleet"
                return
            }
            fleet = value
            error = nil
        } catch { self.error = error.localizedDescription }
    }
}

func gib(_ bytes: Int64) -> String { String(format: "%.2f GB", Double(bytes) / 1_073_741_824) }

struct FleetView: View {
    @StateObject private var model = FleetModel()
    private var compact: Bool { (model.fleet?.phones.filter(\.managed).count ?? 0) > 2 }
    private var columns: [GridItem] { [GridItem(.adaptive(minimum: compact ? 230 : 280, maximum: 360), spacing: 16)] }
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Agent Phone").font(.system(size: 28, weight: .semibold, design: .rounded))
                    Text("Your local simulator fleet").foregroundStyle(.secondary)
                }
                Spacer()
                Button { Task { await model.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                    .help("Refresh fleet").disabled(model.updating)
            }.padding(24)
            if let fleet = model.fleet {
                HStack(spacing: 28) {
                    metric("RUNNING", "\(fleet.phones.filter { $0.state == "Booted" }.count)")
                    metric("SIMULATOR MEMORY", gib(fleet.simulatorMemoryBytes))
                    metric("BOOT BUDGET", gib(fleet.budgetBytes))
                    Spacer()
                    Text("\(gib(fleet.physicalMemoryBytes)) Mac").foregroundStyle(.secondary)
                }.padding(.horizontal, 24).padding(.bottom, 20)
                Divider()
                ScrollView {
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 20) {
                        ForEach(fleet.phones.filter(\.managed).sorted { $0.name < $1.name }) { phone in
                            PhoneCard(phone: phone, cli: model.cli, previewHeight: compact ? 320 : 385)
                        }
                    }.padding(24)
                    if !fleet.phones.contains(where: \.managed) {
                        ContentUnavailableView("No managed phones yet", systemImage: "iphone", description: Text("Run the Wikipedia demo or create a phone through the CLI."))
                    }
                }
                HStack {
                    Image(systemName: "lock.shield")
                    Text("\(fleet.phones.filter { !$0.managed }.count) existing simulators · read only")
                    Spacer()
                    Button("Open evidence") { NSWorkspace.shared.open(URL(fileURLWithPath: fleet.dataDirectory)) }
                }.font(.caption).foregroundStyle(.secondary).padding(16)
            } else {
                Spacer()
                ProgressView("Connecting to local service…").frame(maxWidth: .infinity)
                Spacer()
            }
            if let error = model.error {
                Text(error).font(.callout).foregroundStyle(.red).textSelection(.enabled).padding(16)
            }
        }
        .frame(minWidth: 700, minHeight: 680)
        .background(Color(nsColor: .windowBackgroundColor))
        .task {
            while !Task.isCancelled {
                await model.refresh()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
    func metric(_ title: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title).font(.system(size: 10, weight: .semibold)).foregroundStyle(.secondary)
            Text(value).font(.system(size: 22, weight: .medium, design: .rounded)).monospacedDigit()
        }
    }
}
struct PhoneCard: View {
    @StateObject private var vision = VisionModel()
    let phone: Phone
    let cli: String
    let previewHeight: CGFloat
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Circle().fill(phone.state == "Booted" ? Color.green : Color.gray).frame(width: 7, height: 7)
                Text(phone.name).font(.headline).lineLimit(1)
                Spacer()
            }
            HStack {
                Text(phone.state)
                Spacer()
                if let bytes = phone.memoryBytes { Text(gib(bytes)).monospacedDigit() }
            }.font(.caption).foregroundStyle(.secondary)
            ZStack {
                RoundedRectangle(cornerRadius: 20).fill(.black.opacity(0.06))
                if let image = vision.image {
                    Image(nsImage: image).resizable().scaledToFit().clipShape(RoundedRectangle(cornerRadius: 18))
                } else {
                    VStack(spacing: 10) {
                        Image(systemName: "iphone").font(.system(size: 44, weight: .ultraLight))
                        Text(phone.state == "Booted" ? "Connecting to live screen…" : "Phone is offline").font(.caption)
                    }.foregroundStyle(.secondary)
                }
            }.frame(height: previewHeight)
            Text(phone.owner ?? (phone.busy == true ? "Operation in progress" : "Available to an agent"))
                .font(.caption).lineLimit(1).foregroundStyle(.secondary)
            TimelineView(.periodic(from: .now, by: 0.5)) { timeline in
                HStack {
                    let label = vision.label(at: timeline.date)
                    Circle().fill(label == "Live" ? Color.green : (label == "Stale" ? Color.orange : Color.gray)).frame(width: 6, height: 6)
                    Text(label)
                    Spacer()
                    if let captured = vision.capturedAt {
                        Text(String(format: "%.1fs ago", max(0, timeline.date.timeIntervalSince(captured)))).monospacedDigit()
                    }
                }.font(.caption2).foregroundStyle(.secondary).help(vision.detail ?? "A sampled screen. Unchanged pixels still count as a new capture.")
            }
            Button("Show in Simulator") {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/open")
                process.arguments = ["-a", "Simulator", "--args", "-CurrentDeviceUDID", phone.udid]
                try? process.run()
            }.disabled(phone.state != "Booted")
        }
        .padding(16)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(.primary.opacity(0.07)))
        .onAppear { updateVision() }
        .onChange(of: phone.state) { _, _ in updateVision() }
        .onDisappear { vision.stop() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in vision.stop() }
    }
    private func updateVision() {
        if phone.managed && phone.state == "Booted" { vision.start(cli: cli, device: phone.udid) }
        else { vision.stop() }
    }
}
@main struct AgentPhoneApp: App {
    var body: some Scene {
        WindowGroup { FleetView() }.defaultSize(width: 1120, height: 800)
    }
}
