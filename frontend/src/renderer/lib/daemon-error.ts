// The daemon's locked error envelope is {error, code, message} (httpd/envelope,
// APIError in api/schema.ts); `message` is the sentence the CLI prints, so both
// surfaces say the same thing. Some paths carry only `error`.
export function daemonErrorMessage(body: unknown): string | null {
	if (typeof body !== "object" || body === null) return null;
	const { error, message } = body as { error?: unknown; message?: unknown };
	if (typeof message === "string" && message !== "") return message;
	return typeof error === "string" && error !== "" ? error : null;
}

// A path the daemon already knows is not a failed import — it names a project
// that is already there. The id rides in `details`, so a daemon too old to send
// it (or any other conflict) falls through to the plain message.
export function alreadyRegisteredProjectId(source: unknown): string | null {
	if (typeof source !== "object" || source === null) return null;
	const { code, details } = source as { code?: unknown; details?: unknown };
	if (code !== "PATH_ALREADY_REGISTERED") return null;
	if (typeof details !== "object" || details === null) return null;
	const { existingProjectId } = details as { existingProjectId?: unknown };
	return typeof existingProjectId === "string" && existingProjectId !== "" ? existingProjectId : null;
}
