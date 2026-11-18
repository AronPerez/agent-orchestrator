import EndpointSecurity
import Foundation

enum AOESEventSourceStartError: Error, Equatable {
	case createClient(UInt32)
	case subscribe
}

final class AOEndpointSecurityEventSource: AOESEventSource, @unchecked Sendable {
	private let lock = NSLock()
	private var client: OpaquePointer?
	private var failed = false
	private var onEvent: (@Sendable (AOESEventEnvelope) -> Void)?
	private var onFailure: (@Sendable (AOESFailureCode) -> Void)?

	func start(
		onEvent: @escaping @Sendable (AOESEventEnvelope) -> Void,
		onFailure: @escaping @Sendable (AOESFailureCode) -> Void
	) throws {
		lock.lock()
		precondition(client == nil, "Endpoint Security source started twice")
		self.onEvent = onEvent
		self.onFailure = onFailure
		failed = false
		lock.unlock()

		var createdClient: OpaquePointer?
		let result = es_new_client(&createdClient) { [weak self] _, message in
			guard let self else { return }
			self.handle(message.pointee)
		}
		guard result == ES_NEW_CLIENT_RESULT_SUCCESS, let createdClient else {
			clearHandlers()
			throw AOESEventSourceStartError.createClient(result.rawValue)
		}

		var events = [
			ES_EVENT_TYPE_NOTIFY_FORK,
			ES_EVENT_TYPE_NOTIFY_EXEC,
			ES_EVENT_TYPE_NOTIFY_EXIT,
		]
		let subscribeResult = events.withUnsafeMutableBufferPointer { buffer in
			es_subscribe(createdClient, buffer.baseAddress!, UInt32(buffer.count))
		}
		guard subscribeResult == ES_RETURN_SUCCESS else {
			_ = es_delete_client(createdClient)
			clearHandlers()
			throw AOESEventSourceStartError.subscribe
		}
		lock.lock()
		client = createdClient
		lock.unlock()
	}

	func stop() {
		lock.lock()
		let current = client
		client = nil
		onEvent = nil
		onFailure = nil
		lock.unlock()
		if let current { _ = es_delete_client(current) }
	}

	private func handle(_ message: es_message_t) {
		guard message.version >= 4 else {
			fail(.unsupportedMessageVersion)
			return
		}
		let event: AOESEvent
		switch message.event_type {
		case ES_EVENT_TYPE_NOTIFY_FORK:
			event = .fork(
				parent: identity(message.process),
				child: identity(message.event.fork.child)
			)
		case ES_EVENT_TYPE_NOTIFY_EXEC:
			event = .exec(
				previous: identity(message.process),
				process: identity(message.event.exec.target)
			)
		case ES_EVENT_TYPE_NOTIFY_EXIT:
			event = .exit(
				process: identity(message.process),
				status: Int32(message.event.exit.stat)
			)
		default:
			return
		}

		lock.lock()
		let handler = failed ? nil : onEvent
		lock.unlock()
		handler?(AOESEventEnvelope(sourceSequence: message.global_seq_num, event: event))
	}

	private func identity(_ process: UnsafePointer<es_process_t>) -> AOESProcessIdentity {
		let token = process.pointee.audit_token
		return AOESProcessIdentity(
			pid: audit_token_to_pid(token),
			pidVersion: UInt32(bitPattern: audit_token_to_pidversion(token))
		)
	}

	private func fail(_ failure: AOESFailureCode) {
		lock.lock()
		guard !failed else {
			lock.unlock()
			return
		}
		failed = true
		let handler = onFailure
		lock.unlock()
		handler?(failure)
	}

	private func clearHandlers() {
		lock.lock()
		onEvent = nil
		onFailure = nil
		lock.unlock()
	}

	deinit {
		stop()
	}
}
