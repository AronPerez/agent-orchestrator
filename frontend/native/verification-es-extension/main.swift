import Foundation

#if !AO_MACOS_ES_EXTENSION_DEV
#error("The Endpoint Security prototype must only be built with AO_MACOS_ES_EXTENSION_DEV")
#endif

final class AOEndpointSecurityExtensionRuntime {
	private let engine: AOESObservationEngine
	private let listener: NSXPCListener
	private let listenerDelegate: AOESXPCListenerDelegate

	init() throws {
		let tracker = AOESObservationTracker()
		let source = AOEndpointSecurityEventSource()
		engine = AOESObservationEngine(source: source, tracker: tracker)
		let teamIdentifier = try AOESPeerRequirement.currentTeamIdentifier()
		let supervisorRequirement = try AOESPeerRequirement.supervisor(teamIdentifier: teamIdentifier)
		listener = NSXPCListener(machServiceName: AOESProtocol.machServiceName)
		listenerDelegate = AOESXPCListenerDelegate(
			tracker: tracker,
			supervisorRequirement: supervisorRequirement
		)
		listener.delegate = listenerDelegate
	}

	func run() throws -> Never {
		try engine.start()
		listener.activate()
		dispatchMain()
	}
}

do {
	let runtime = try AOEndpointSecurityExtensionRuntime()
	try runtime.run()
} catch {
	let message = "verification Endpoint Security extension failed to start: \(error)\n"
	FileHandle.standardError.write(Data(message.utf8))
	exit(1)
}
