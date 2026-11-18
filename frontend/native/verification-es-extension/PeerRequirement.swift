import Foundation
import Security

enum AOESPeerRequirementError: Error, Equatable {
	case invalidTeamIdentifier
	case signingInformationUnavailable(OSStatus)
	case teamIdentifierMissing
}

enum AOESPeerRequirement {
	static func supervisor(teamIdentifier: String) throws -> String {
		try validate(teamIdentifier)
		return "anchor apple generic and identifier \"\(AOESProtocol.supervisorSigningIdentifier)\" and certificate leaf[subject.OU] = \"\(teamIdentifier)\""
	}

	static func endpointSecurityExtension(teamIdentifier: String) throws -> String {
		try validate(teamIdentifier)
		return "anchor apple generic and identifier \"\(AOESProtocol.extensionBundleIdentifier)\" and certificate leaf[subject.OU] = \"\(teamIdentifier)\" and entitlement[\"\(AOESProtocol.endpointSecurityEntitlement)\"] exists"
	}

	static func currentTeamIdentifier() throws -> String {
		var code: SecCode?
		let copySelfStatus = SecCodeCopySelf(SecCSFlags(), &code)
		guard copySelfStatus == errSecSuccess, let code else {
			throw AOESPeerRequirementError.signingInformationUnavailable(copySelfStatus)
		}
		var staticCode: SecStaticCode?
		let copyStaticStatus = SecCodeCopyStaticCode(code, SecCSFlags(), &staticCode)
		guard copyStaticStatus == errSecSuccess, let staticCode else {
			throw AOESPeerRequirementError.signingInformationUnavailable(copyStaticStatus)
		}
		var information: CFDictionary?
		let informationStatus = SecCodeCopySigningInformation(staticCode, SecCSFlags(rawValue: kSecCSSigningInformation), &information)
		guard informationStatus == errSecSuccess,
			let values = information as? [String: Any]
		else {
			throw AOESPeerRequirementError.signingInformationUnavailable(informationStatus)
		}
		guard let teamIdentifier = values[kSecCodeInfoTeamIdentifier as String] as? String else {
			throw AOESPeerRequirementError.teamIdentifierMissing
		}
		try validate(teamIdentifier)
		return teamIdentifier
	}

	private static func validate(_ teamIdentifier: String) throws {
		guard teamIdentifier.utf8.count == 10,
			teamIdentifier.utf8.allSatisfy({ ($0 >= 48 && $0 <= 57) || ($0 >= 65 && $0 <= 90) })
		else {
			throw AOESPeerRequirementError.invalidTeamIdentifier
		}
	}
}
