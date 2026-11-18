import Foundation

protocol AOESEventSource: AnyObject {
	func start(
		onEvent: @escaping @Sendable (AOESEventEnvelope) -> Void,
		onFailure: @escaping @Sendable (AOESFailureCode) -> Void
	) throws
	func stop()
}

final class AOESObservationEngine {
	private let source: AOESEventSource
	private let tracker: AOESObservationTracker

	init(source: AOESEventSource, tracker: AOESObservationTracker) {
		self.source = source
		self.tracker = tracker
	}

	func start() throws {
		try source.start(
			onEvent: { [tracker] event in tracker.consume(event) },
			onFailure: { [tracker] failure in tracker.sourceFailed(failure) }
		)
	}

	func stop() {
		source.stop()
	}
}

final class AOESObservationTracker: @unchecked Sendable {
	typealias Delivery = @Sendable (AOESObservation) -> Void

	private struct Run {
		let connectionID: UUID
		let deliver: Delivery
		var identities: Set<AOESProcessIdentity>
		var nextSequence: UInt64
	}

	private struct PendingDelivery {
		let deliver: Delivery
		let observation: AOESObservation
	}

	let sourceID: UUID

	private let lock = NSLock()
	private var runs: [UUID: Run] = [:]
	private var lastSourceSequence: UInt64?
	private var sourceFailure: AOESFailureCode?

	init(sourceID: UUID = UUID()) {
		self.sourceID = sourceID
	}

	func register(
		_ request: AOESRegisterRootRequest,
		connectionID: UUID,
		deliver: @escaping Delivery
	) -> AOESRegistrationReply {
		guard request.version == AOESProtocol.version else {
			return registrationReply(accepted: false, error: .unsupportedProtocolVersion)
		}
		guard request.root.isValid else {
			return registrationReply(accepted: false, error: .invalidRoot)
		}

		lock.lock()
		defer { lock.unlock() }
		guard sourceFailure == nil else {
			return registrationReply(accepted: false, error: .sourceUnavailable)
		}
		guard runs[request.runID] == nil else {
			return registrationReply(accepted: false, error: .runAlreadyRegistered)
		}
		runs[request.runID] = Run(
			connectionID: connectionID,
			deliver: deliver,
			identities: [request.root],
			nextSequence: 1
		)
		return registrationReply(accepted: true, error: nil)
	}

	func unregister(_ request: AOESUnregisterRunRequest, connectionID: UUID) -> Bool {
		guard request.version == AOESProtocol.version else { return false }
		lock.lock()
		defer { lock.unlock() }
		guard runs[request.runID]?.connectionID == connectionID else { return false }
		runs.removeValue(forKey: request.runID)
		return true
	}

	@discardableResult
	func connectionClosed(_ connectionID: UUID) -> [UUID] {
		lock.lock()
		defer { lock.unlock() }
		let affected = runs.compactMap { $0.value.connectionID == connectionID ? $0.key : nil }
		for runID in affected { runs.removeValue(forKey: runID) }
		return affected
	}

	func consume(_ envelope: AOESEventEnvelope) {
		let pending: [PendingDelivery]
		lock.lock()
		if let failure = sequenceFailure(for: envelope.sourceSequence) {
			pending = failLocked(failure, sourceSequence: envelope.sourceSequence)
		} else if sourceFailure != nil {
			pending = []
		} else {
			lastSourceSequence = envelope.sourceSequence
			pending = consumeLocked(envelope)
		}
		lock.unlock()
		deliver(pending)
	}

	func sourceFailed(_ failure: AOESFailureCode) {
		lock.lock()
		let pending = failLocked(failure, sourceSequence: lastSourceSequence ?? 0)
		lock.unlock()
		deliver(pending)
	}

	private func registrationReply(accepted: Bool, error: AOESRegistrationError?) -> AOESRegistrationReply {
		AOESRegistrationReply(
			version: AOESProtocol.version,
			sourceID: sourceID,
			accepted: accepted,
			nextSequence: 1,
			error: error
		)
	}

	private func sequenceFailure(for current: UInt64) -> AOESFailureCode? {
		guard sourceFailure == nil, let previous = lastSourceSequence else { return nil }
		let (expected, overflow) = previous.addingReportingOverflow(1)
		if overflow || current <= previous { return .sourceSequenceInvalid }
		if current != expected { return .sourceSequenceGap }
		return nil
	}

	private func consumeLocked(_ envelope: AOESEventEnvelope) -> [PendingDelivery] {
		var pending: [PendingDelivery] = []
		for runID in runs.keys.sorted(by: { $0.uuidString < $1.uuidString }) {
			guard var run = runs[runID] else { continue }
			let observation: AOESObservation?
			switch envelope.event {
			case let .fork(parent, child) where run.identities.contains(parent):
				run.identities.insert(child)
				observation = makeObservation(
					runID: runID, run: run, sourceSequence: envelope.sourceSequence,
					kind: .fork, process: child, parent: parent
				)
			case let .exec(previous, process) where run.identities.remove(previous) != nil:
				run.identities.insert(process)
				observation = makeObservation(
					runID: runID, run: run, sourceSequence: envelope.sourceSequence,
					kind: .exec, process: process, previous: previous
				)
			case let .exit(process, status) where run.identities.remove(process) != nil:
				observation = makeObservation(
					runID: runID, run: run, sourceSequence: envelope.sourceSequence,
					kind: .exit, process: process, exitStatus: status
				)
			default:
				observation = nil
			}
			if let observation {
				run.nextSequence += 1
				runs[runID] = run
				pending.append(PendingDelivery(deliver: run.deliver, observation: observation))
			}
		}
		return pending
	}

	private func failLocked(_ failure: AOESFailureCode, sourceSequence: UInt64) -> [PendingDelivery] {
		guard sourceFailure == nil else { return [] }
		sourceFailure = failure
		let pending = runs.map { runID, run in
			PendingDelivery(
				deliver: run.deliver,
				observation: makeObservation(
					runID: runID, run: run, sourceSequence: sourceSequence,
					kind: .sourceFailure, failure: failure
				)
			)
		}
		runs.removeAll()
		return pending
	}

	private func makeObservation(
		runID: UUID,
		run: Run,
		sourceSequence: UInt64,
		kind: AOESObservationKind,
		process: AOESProcessIdentity? = nil,
		parent: AOESProcessIdentity? = nil,
		previous: AOESProcessIdentity? = nil,
		exitStatus: Int32? = nil,
		failure: AOESFailureCode? = nil
	) -> AOESObservation {
		AOESObservation(
			version: AOESProtocol.version,
			sourceID: sourceID,
			runID: runID,
			sequence: run.nextSequence,
			sourceSequence: sourceSequence,
			kind: kind,
			process: process,
			parent: parent,
			previous: previous,
			exitStatus: exitStatus,
			failure: failure
		)
	}

	private func deliver(_ pending: [PendingDelivery]) {
		for item in pending { item.deliver(item.observation) }
	}
}
