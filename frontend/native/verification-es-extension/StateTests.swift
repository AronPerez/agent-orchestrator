import Foundation
import Security

private enum TestFailure: Error, CustomStringConvertible {
	case failed(String)

	var description: String {
		switch self { case let .failed(message): message }
	}
}

private func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
	guard condition() else { throw TestFailure.failed(message) }
}

private final class FakeEventSource: AOESEventSource {
	private var onEvent: (@Sendable (AOESEventEnvelope) -> Void)?
	private var onFailure: (@Sendable (AOESFailureCode) -> Void)?

	func start(
		onEvent: @escaping @Sendable (AOESEventEnvelope) -> Void,
		onFailure: @escaping @Sendable (AOESFailureCode) -> Void
	) throws {
		self.onEvent = onEvent
		self.onFailure = onFailure
	}

	func stop() {}
	func emit(_ event: AOESEventEnvelope) { onEvent?(event) }
	func fail(_ failure: AOESFailureCode) { onFailure?(failure) }
}

private final class ObservationRecorder: @unchecked Sendable {
	private let lock = NSLock()
	private var stored: [AOESObservation] = []

	func append(_ observation: AOESObservation) {
		lock.lock()
		stored.append(observation)
		lock.unlock()
	}

	var observations: [AOESObservation] {
		lock.lock()
		defer { lock.unlock() }
		return stored
	}
}

@main
private enum StateTests {
	static func main() throws {
		try testRegistrationAndOrderedLifecycle()
		try testFailureBeforeRegistrationAcknowledgementIsBuffered()
		try testSequenceGapFailsClosed()
		try testDuplicateSequenceFailsClosed()
		try testSourceFailureFailsClosed()
		try testStableIdentityRejectsPIDReuse()
		try testRegistrationValidationAndDisconnect()
		try testWireBoundsAndRoundTrip()
		try testPeerRequirements()
		print("verification ES state tests passed")
	}

	private static func testRegistrationAndOrderedLifecycle() throws {
		let sourceID = UUID(uuidString: "00000000-0000-0000-0000-000000000001")!
		let runID = UUID(uuidString: "00000000-0000-0000-0000-000000000002")!
		let connectionID = UUID()
		let root = AOESProcessIdentity(pid: 100, pidVersion: 1)
		let child = AOESProcessIdentity(pid: 101, pidVersion: 1)
		let executed = AOESProcessIdentity(pid: 101, pidVersion: 2)
		let tracker = AOESObservationTracker(sourceID: sourceID)
		let source = FakeEventSource()
		let engine = AOESObservationEngine(source: source, tracker: tracker)
		let recorder = ObservationRecorder()
		try engine.start()
		let reply = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID,
			deliver: recorder.append
		)
		try expect(reply.accepted && reply.sourceID == sourceID && reply.nextSequence == 1, "root registration was not acknowledged")
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "registered root could not be acknowledged")

		source.emit(AOESEventEnvelope(sourceSequence: 20, event: .fork(parent: root, child: child)))
		source.emit(AOESEventEnvelope(sourceSequence: 21, event: .exec(previous: child, process: executed)))
		source.emit(AOESEventEnvelope(sourceSequence: 22, event: .exit(process: executed, status: 0)))

		let observations = recorder.observations
		try expect(observations.map(\.kind) == [.fork, .exec, .exit], "lifecycle observations were not ordered")
		try expect(observations.map(\.sequence) == [1, 2, 3], "run sequence was not contiguous")
		try expect(observations.map(\.sourceSequence) == [20, 21, 22], "source sequence was not preserved")
		try expect(observations[0].parent == root && observations[0].process == child, "fork identities were wrong")
		try expect(observations[1].previous == child && observations[1].process == executed, "exec identities were wrong")
	}

	private static func testFailureBeforeRegistrationAcknowledgementIsBuffered() throws {
		let tracker = AOESObservationTracker()
		let root = AOESProcessIdentity(pid: 150, pidVersion: 1)
		let runID = UUID()
		let connectionID = UUID()
		let recorder = ObservationRecorder()
		let reply = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID,
			deliver: recorder.append
		)
		try expect(reply.accepted, "root registration failed")

		tracker.sourceFailed(.sourceDisconnected)
		try expect(recorder.observations.isEmpty, "source failure escaped before registration acknowledgement")
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "failed registration could not be acknowledged")
		try expect(recorder.observations.count == 1, "buffered source failure was not delivered")
		try expect(recorder.observations[0].failure == .sourceDisconnected, "buffered source failure changed")
		try expect(!tracker.acknowledge(runID, connectionID: connectionID), "terminal registration remained active")
	}

	private static func testSequenceGapFailsClosed() throws {
		let tracker = AOESObservationTracker()
		let source = FakeEventSource()
		let engine = AOESObservationEngine(source: source, tracker: tracker)
		let root = AOESProcessIdentity(pid: 200, pidVersion: 1)
		let runID = UUID()
		let connectionID = UUID()
		let recorder = ObservationRecorder()
		try engine.start()
		_ = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID, deliver: recorder.append
		)
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "gap test registration was not acknowledged")
		source.emit(AOESEventEnvelope(sourceSequence: 7, event: .fork(parent: root, child: .init(pid: 201, pidVersion: 1))))
		source.emit(AOESEventEnvelope(sourceSequence: 9, event: .exit(process: root, status: 0)))

		try expect(recorder.observations.map(\.kind) == [.fork, .sourceFailure], "gap did not fail the run")
		try expect(recorder.observations.last?.failure == .sourceSequenceGap, "wrong gap failure code")
		let rejected = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: UUID(), root: root),
			connectionID: UUID(), deliver: { _ in }
		)
		try expect(!rejected.accepted && rejected.error == .sourceUnavailable, "failed source accepted another root")
	}

	private static func testDuplicateSequenceFailsClosed() throws {
		let tracker = AOESObservationTracker()
		let root = AOESProcessIdentity(pid: 300, pidVersion: 1)
		let runID = UUID()
		let connectionID = UUID()
		let recorder = ObservationRecorder()
		_ = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID, deliver: recorder.append
		)
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "duplicate test registration was not acknowledged")
		tracker.consume(.init(sourceSequence: 4, event: .fork(parent: root, child: .init(pid: 301, pidVersion: 1))))
		tracker.consume(.init(sourceSequence: 4, event: .exit(process: root, status: 0)))
		try expect(recorder.observations.last?.failure == .sourceSequenceInvalid, "duplicate sequence did not fail closed")
	}

	private static func testSourceFailureFailsClosed() throws {
		let tracker = AOESObservationTracker()
		let source = FakeEventSource()
		let engine = AOESObservationEngine(source: source, tracker: tracker)
		let root = AOESProcessIdentity(pid: 350, pidVersion: 1)
		let runID = UUID()
		let connectionID = UUID()
		let recorder = ObservationRecorder()
		try engine.start()
		_ = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID, deliver: recorder.append
		)
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "source-failure test registration was not acknowledged")
		source.fail(.sourceDisconnected)
		try expect(recorder.observations.count == 1, "source failure did not terminate the run")
		try expect(recorder.observations[0].failure == .sourceDisconnected, "source failure code changed")
	}

	private static func testStableIdentityRejectsPIDReuse() throws {
		let tracker = AOESObservationTracker()
		let root = AOESProcessIdentity(pid: 400, pidVersion: 8)
		let runID = UUID()
		let connectionID = UUID()
		let recorder = ObservationRecorder()
		_ = tracker.register(
			AOESRegisterRootRequest(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID, deliver: recorder.append
		)
		try expect(tracker.acknowledge(runID, connectionID: connectionID), "identity test registration was not acknowledged")
		tracker.consume(.init(sourceSequence: 1, event: .exit(process: .init(pid: 400, pidVersion: 9), status: 0)))
		try expect(recorder.observations.isEmpty, "PID reuse matched an earlier process execution")
	}

	private static func testRegistrationValidationAndDisconnect() throws {
		let tracker = AOESObservationTracker()
		let connectionID = UUID()
		let runID = UUID()
		let root = AOESProcessIdentity(pid: 500, pidVersion: 1)
		let badVersion = tracker.register(
			.init(version: AOESProtocol.version + 1, runID: runID, root: root),
			connectionID: connectionID, deliver: { _ in }
		)
		try expect(badVersion.error == .unsupportedProtocolVersion, "unsupported protocol version was accepted")
		let badRoot = tracker.register(
			.init(version: AOESProtocol.version, runID: runID, root: .init(pid: 0, pidVersion: 1)),
			connectionID: connectionID, deliver: { _ in }
		)
		try expect(badRoot.error == .invalidRoot, "invalid root was accepted")
		let accepted = tracker.register(
			.init(version: AOESProtocol.version, runID: runID, root: root),
			connectionID: connectionID, deliver: { _ in }
		)
		try expect(accepted.accepted, "valid root was rejected")
		try expect(tracker.connectionClosed(connectionID) == [runID], "disconnect did not invalidate its run")
	}

	private static func testWireBoundsAndRoundTrip() throws {
		let request = AOESRegisterRootRequest(
			version: AOESProtocol.version,
			runID: UUID(),
			root: .init(pid: 600, pidVersion: 2)
		)
		let encoded = try AOESWireCodec.encode(request)
		let decoded = try AOESWireCodec.decode(AOESRegisterRootRequest.self, from: encoded)
		try expect(decoded == request, "wire round trip changed the request")
		let oversized = Data(repeating: 0, count: AOESProtocol.maximumMessageBytes + 1)
		do {
			_ = try AOESWireCodec.decode(AOESRegisterRootRequest.self, from: oversized)
			throw TestFailure.failed("oversized wire message was accepted")
		} catch AOESWireError.messageTooLarge {}
	}

	private static func testPeerRequirements() throws {
		let teamIdentifier = "A1B2C3D4E5"
		let supervisor = try AOESPeerRequirement.supervisor(teamIdentifier: teamIdentifier)
		try expect(supervisor.contains("certificate leaf[subject.OU] = \"\(teamIdentifier)\""), "supervisor requirement omitted the team")
		try expect(supervisor.contains("identifier \"\(AOESProtocol.supervisorSigningIdentifier)\""), "supervisor requirement omitted its signing identifier")
		let endpointSecurity = try AOESPeerRequirement.endpointSecurityExtension(teamIdentifier: teamIdentifier)
		try expect(endpointSecurity.contains("identifier \"\(AOESProtocol.extensionBundleIdentifier)\""), "extension requirement omitted its bundle identifier")
		try expect(endpointSecurity.contains("entitlement[\"\(AOESProtocol.endpointSecurityEntitlement)\"] exists"), "extension requirement omitted its entitlement")
		for requirementText in [supervisor, endpointSecurity] {
			var requirement: SecRequirement?
			let status = SecRequirementCreateWithString(requirementText as CFString, SecCSFlags(), &requirement)
			try expect(
				status == errSecSuccess && requirement != nil,
				"Security.framework rejected peer requirement \(requirementText) with status \(status)"
			)
		}
		do {
			_ = try AOESPeerRequirement.supervisor(teamIdentifier: "not a team")
			throw TestFailure.failed("invalid team identifier was accepted")
		} catch AOESPeerRequirementError.invalidTeamIdentifier {}
	}
}
