export const EDITOR_IDS = [
	"cursor",
	"vscode",
	"windsurf",
	"zed",
	"trae",
	"kiro",
	"positron",
	"vscodium",
	"vscode-insiders",
	"sublime",
	"intellij",
	"webstorm",
	"pycharm",
	"goland",
	"phpstorm",
	"rubymine",
	"clion",
	"rider",
	"android-studio",
	"fleet",
] as const;

export type EditorId = (typeof EDITOR_IDS)[number];
export type OpenTargetId = EditorId | "file-manager" | "terminal";
export type OpenTargetKind = "editor" | "file_manager" | "terminal";

export type OpenTarget = {
	id: OpenTargetId;
	name: string;
	kind: OpenTargetKind;
};

export type EditorHandoffState = {
	targets: OpenTarget[];
	preferredEditorId: EditorId;
	workspaceAvailable: boolean;
	unavailableReason?: string;
	/**
	 * Present when the session lives on a remote host. The renderer renders
	 * remote-ness from this block — it never receives a filesystem path.
	 */
	remote?: {
		hostLabel: string;
		/** An SSH destination is saved for the host, so remote open can work. */
		sshConfigured: boolean;
	};
};

export type EditorHandoffStateInput = {
	/** "local" for the local daemon, else the saved host url. */
	host: string;
	sessionId: string;
};

export type OpenSessionTargetInput = {
	host: string;
	sessionId: string;
	targetId?: OpenTargetId;
};

export type OpenSessionTargetResult = OpenTarget;

const editorIDSet = new Set<string>(EDITOR_IDS);

export function isEditorId(value: unknown): value is EditorId {
	return typeof value === "string" && editorIDSet.has(value);
}
