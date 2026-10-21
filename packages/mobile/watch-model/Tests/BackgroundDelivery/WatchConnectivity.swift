import Foundation

@objc public enum WCSessionActivationState: Int { case notActivated, inactive, activated }
public protocol WCSessionDelegate: AnyObject {}

public class WCSession: NSObject {
    public static let `default` = WCSession()
    public static func isSupported() -> Bool { true }
    public weak var delegate: WCSessionDelegate?
    @objc dynamic public var activationState = WCSessionActivationState.notActivated
    @objc dynamic public var hasContentPending = false
    public var receivedApplicationContext: [String: Any] = [:]
    public var isReachable = false
    public private(set) var activations = 0
    public func activate() { activations += 1 }
    public func sendMessage(_ message: [String: Any], replyHandler: (([String: Any]) -> Void)?, errorHandler: ((Error) -> Void)?) {
        preconditionFailure("Background snapshot delivery must not send a wrist action")
    }
}
