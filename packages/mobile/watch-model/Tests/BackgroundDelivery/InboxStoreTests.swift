import Foundation
import WatchConnectivity
import WidgetKit

// Snapshot semantics have their own Foundation suite; these doubles keep lifecycle
// tests off the real Watch app-group preferences while exercising InboxStore itself.
struct WatchSnapshot: Codable {
    var version: Int
    var generatedAt: Double
    func retainingCache(from previous: WatchSnapshot?) -> WatchSnapshot { self }
}
enum WatchCache {
    static var saved: WatchSnapshot?
    static func load() -> WatchSnapshot? { saved }
    static func save(_ snapshot: WatchSnapshot) { saved = snapshot }
}

@main
struct InboxStoreTests {
    @MainActor static func main() async throws {
        let session = WCSession.default
        let store = InboxStore()
        assert(session.delegate === store && session.activations == 1)
        var completed = false
        let delivery = Task { await store.receiveBackgroundUpdates(); completed = true }
        try await Task.sleep(for: .milliseconds(20))
        assert(!completed && WatchCache.saved == nil)

        session.hasContentPending = true
        session.activationState = .activated
        try await Task.sleep(for: .milliseconds(20))
        assert(!completed && WatchCache.saved == nil)

        // No foreground scene is running; a delivered snapshot alone must not
        // release the task while WatchConnectivity still has content pending.
        session.receivedApplicationContext = ["snapshot": "{\"version\":1,\"generatedAt\":123}"]
        store.session(session, didReceiveApplicationContext: session.receivedApplicationContext)
        try await Task.sleep(for: .milliseconds(20))
        assert(!completed)
        session.hasContentPending = false
        for _ in 0..<100 where !completed { try await Task.sleep(for: .milliseconds(10)) }
        assert(completed)
        await delivery.value
        assert(WatchCache.saved?.generatedAt == 123)
        assert(store.snapshot?.generatedAt == 123)
        assert(WidgetCenter.shared.reloads.last == "AOAttention")

        // Already-drained content must be saved before the handler returns,
        // even when its delegate callback has not reached the main queue yet.
        session.receivedApplicationContext = ["snapshot": "{\"version\":1,\"generatedAt\":456}"]
        await store.receiveBackgroundUpdates()
        assert(WatchCache.saved?.generatedAt == 456)
        store.session(session, didReceiveApplicationContext: ["snapshot": "{\"version\":1,\"generatedAt\":123}"])
        try await Task.sleep(for: .milliseconds(20))
        assert(WatchCache.saved?.generatedAt == 456)

        // The OS may cancel the background budget before activation/delivery.
        session.activationState = .notActivated
        session.hasContentPending = true
        var cancelled = false
        let pending = Task { await store.receiveBackgroundUpdates(); cancelled = true }
        try await Task.sleep(for: .milliseconds(20))
        pending.cancel()
        for _ in 0..<100 where !cancelled { try await Task.sleep(for: .milliseconds(10)) }
        assert(cancelled && WatchCache.saved?.generatedAt == 456)
        await pending.value
        print("Watch background delivery checks passed")
    }
}
