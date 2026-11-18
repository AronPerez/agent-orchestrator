import Foundation

enum AOESProtocol {
	static let version: UInt16 = 1
	static let extensionBundleIdentifier = "dev.agent-orchestrator.verification.endpoint-security"
	static let machServiceName = "dev.agent-orchestrator.verification.endpoint-security.xpc"
	static let supervisorSigningIdentifier = "dev.agent-orchestrator.verification-supervisor"
	static let endpointSecurityEntitlement = "com.apple.developer.endpoint-security.client"
	static let maximumMessageBytes = 16 * 1024
}

struct AOESProcessIdentity: Codable, Hashable, Sendable {
	let pid: Int32
	let pidVersion: UInt32

	var isValid: Bool { pid > 0 }
}

enum AOESObservationKind: String, Codable, Sendable {
	case fork
	case exec
	case exit
	case sourceFailure
}

enum AOESFailureCode: String, Codable, Sendable {
	case sourceSequenceGap
	case sourceSequenceInvalid
	case sourceDisconnected
	case unsupportedMessageVersion
}

enum AOESRegistrationError: String, Codable, Sendable {
	case unsupportedProtocolVersion
	case invalidMessage
	case invalidRoot
	case runAlreadyRegistered
	case sourceUnavailable
}

struct AOESRegisterRootRequest: Codable, Equatable, Sendable {
	let version: UInt16
	let runID: UUID
	let root: AOESProcessIdentity
}

struct AOESRegistrationReply: Codable, Equatable, Sendable {
	let version: UInt16
	let sourceID: UUID
	let accepted: Bool
	let nextSequence: UInt64
	let error: AOESRegistrationError?
}

struct AOESReadyForObservationsRequest: Codable, Equatable, Sendable {
	let version: UInt16
	let runID: UUID
}

struct AOESUnregisterRunRequest: Codable, Equatable, Sendable {
	let version: UInt16
	let runID: UUID
}

struct AOESObservation: Codable, Equatable, Sendable {
	let version: UInt16
	let sourceID: UUID
	let runID: UUID
	// Sequence on this run's XPC stream. Unlike sourceSequence, this is contiguous.
	let sequence: UInt64
	// Endpoint Security's global per-client sequence, checked inside the extension.
	let sourceSequence: UInt64
	let kind: AOESObservationKind
	let process: AOESProcessIdentity?
	let parent: AOESProcessIdentity?
	let previous: AOESProcessIdentity?
	let exitStatus: Int32?
	let failure: AOESFailureCode?
}

enum AOESEvent: Equatable, Sendable {
	case fork(parent: AOESProcessIdentity, child: AOESProcessIdentity)
	case exec(previous: AOESProcessIdentity, process: AOESProcessIdentity)
	case exit(process: AOESProcessIdentity, status: Int32)
}

struct AOESEventEnvelope: Equatable, Sendable {
	let sourceSequence: UInt64
	let event: AOESEvent
}
