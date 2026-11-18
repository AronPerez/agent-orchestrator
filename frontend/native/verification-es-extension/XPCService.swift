import Foundation

@objc protocol AOESControlXPCProtocol {
	func registerRoot(_ payload: Data, withReply reply: @escaping (Data) -> Void)
	func unregisterRun(_ payload: Data, withReply reply: @escaping (Bool) -> Void)
}

@objc protocol AOESObservationXPCProtocol {
	func receiveObservation(_ payload: Data)
}

final class AOESXPCConnectionService: NSObject, AOESControlXPCProtocol, @unchecked Sendable {
	private let connectionID: UUID
	private let tracker: AOESObservationTracker
	private weak var connection: NSXPCConnection?

	init(connectionID: UUID, tracker: AOESObservationTracker, connection: NSXPCConnection) {
		self.connectionID = connectionID
		self.tracker = tracker
		self.connection = connection
	}

	func registerRoot(_ payload: Data, withReply reply: @escaping (Data) -> Void) {
		let request: AOESRegisterRootRequest
		do {
			request = try AOESWireCodec.decode(AOESRegisterRootRequest.self, from: payload)
		} catch {
			reply(encodedRegistrationError(.invalidMessage))
			return
		}
		let registration = tracker.register(request, connectionID: connectionID) { [weak self] observation in
			self?.send(observation)
		}
		do {
			reply(try AOESWireCodec.encode(registration))
		} catch {
			_ = tracker.unregister(
				AOESUnregisterRunRequest(version: AOESProtocol.version, runID: request.runID),
				connectionID: connectionID
			)
			reply(encodedRegistrationError(.invalidMessage))
		}
	}

	func unregisterRun(_ payload: Data, withReply reply: @escaping (Bool) -> Void) {
		guard let request = try? AOESWireCodec.decode(AOESUnregisterRunRequest.self, from: payload) else {
			reply(false)
			return
		}
		reply(tracker.unregister(request, connectionID: connectionID))
	}

	private func send(_ observation: AOESObservation) {
		guard let payload = try? AOESWireCodec.encode(observation), let connection else {
			tracker.connectionClosed(connectionID)
			return
		}
		let proxy = connection.remoteObjectProxyWithErrorHandler { [weak self] _ in
			guard let self else { return }
			self.tracker.connectionClosed(self.connectionID)
		} as? AOESObservationXPCProtocol
		guard let proxy else {
			tracker.connectionClosed(connectionID)
			return
		}
		proxy.receiveObservation(payload)
	}

	private func encodedRegistrationError(_ error: AOESRegistrationError) -> Data {
		let registration = AOESRegistrationReply(
			version: AOESProtocol.version,
			sourceID: tracker.sourceID,
			accepted: false,
			nextSequence: 1,
			error: error
		)
		return (try? AOESWireCodec.encode(registration)) ?? Data()
	}
}

final class AOESXPCListenerDelegate: NSObject, NSXPCListenerDelegate, @unchecked Sendable {
	private let tracker: AOESObservationTracker
	private let supervisorRequirement: String

	init(tracker: AOESObservationTracker, supervisorRequirement: String) {
		self.tracker = tracker
		self.supervisorRequirement = supervisorRequirement
	}

	func listener(_ listener: NSXPCListener, shouldAcceptNewConnection connection: NSXPCConnection) -> Bool {
		// Foundation rejects messages from peers outside AO's signing team or
		// without the supervisor entitlement before dispatching exported methods.
		connection.setCodeSigningRequirement(supervisorRequirement)
		let connectionID = UUID()
		let service = AOESXPCConnectionService(connectionID: connectionID, tracker: tracker, connection: connection)
		connection.exportedInterface = NSXPCInterface(with: AOESControlXPCProtocol.self)
		connection.exportedObject = service
		connection.remoteObjectInterface = NSXPCInterface(with: AOESObservationXPCProtocol.self)
		connection.invalidationHandler = { [tracker] in
			tracker.connectionClosed(connectionID)
		}
		connection.interruptionHandler = { [tracker] in
			tracker.connectionClosed(connectionID)
		}
		connection.activate()
		return true
	}
}
