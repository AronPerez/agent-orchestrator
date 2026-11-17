/** An old preload, missing handler or hung main must not mount mixed builds. */
export async function verifyDesktopBuild(verify: (identity: string) => Promise<boolean>, identity: string): Promise<boolean> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		return await Promise.race([
			verify(identity),
			new Promise<false>((resolve) => { timer = setTimeout(() => resolve(false), 5_000); }),
		]);
	} catch { return false; }
	finally { clearTimeout(timer); }
}
