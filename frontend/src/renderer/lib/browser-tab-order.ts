import { arrayMove } from "@dnd-kit/sortable";

export const MAX_BROWSER_TABS = 16;

export function reorderBrowserTabs(ids: string[], activeId: string, overId: string): string[] | null {
	const oldIndex = ids.indexOf(activeId);
	const newIndex = ids.indexOf(overId);
	if (oldIndex === -1 || newIndex === -1 || oldIndex === newIndex) return null;
	return arrayMove(ids, oldIndex, newIndex);
}
