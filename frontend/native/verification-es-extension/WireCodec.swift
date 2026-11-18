import Foundation

enum AOESWireError: Error, Equatable {
	case messageTooLarge
}

enum AOESWireCodec {
	static func encode<T: Encodable>(_ value: T) throws -> Data {
		let encoder = JSONEncoder()
		encoder.outputFormatting = [.sortedKeys]
		let data = try encoder.encode(value)
		guard data.count <= AOESProtocol.maximumMessageBytes else {
			throw AOESWireError.messageTooLarge
		}
		return data
	}

	static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
		guard data.count <= AOESProtocol.maximumMessageBytes else {
			throw AOESWireError.messageTooLarge
		}
		return try JSONDecoder().decode(type, from: data)
	}
}
