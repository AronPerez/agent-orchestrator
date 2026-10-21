public final class WidgetCenter {
    public static let shared = WidgetCenter()
    public var reloads: [String] = []
    public func reloadTimelines(ofKind kind: String) { reloads.append(kind) }
}
