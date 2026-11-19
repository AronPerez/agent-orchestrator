import * as React from "react";
import { RadioGroup as RadioGroupPrimitive } from "radix-ui";

import { cn } from "../../lib/utils";

/** shadcn-style radio group over Radix: arrow keys move between items. */
const RadioGroup = React.forwardRef<
	React.ElementRef<typeof RadioGroupPrimitive.Root>,
	React.ComponentPropsWithoutRef<typeof RadioGroupPrimitive.Root>
>(({ className, ...props }, ref) => (
	<RadioGroupPrimitive.Root data-slot="radio-group" className={cn("grid gap-2", className)} ref={ref} {...props} />
));
RadioGroup.displayName = "RadioGroup";

const RadioGroupItem = React.forwardRef<
	React.ElementRef<typeof RadioGroupPrimitive.Item>,
	React.ComponentPropsWithoutRef<typeof RadioGroupPrimitive.Item>
>(({ className, ...props }, ref) => (
	<RadioGroupPrimitive.Item
		data-slot="radio-group-item"
		className={cn(
			"grid size-3.5 shrink-0 place-items-center rounded-full border border-border-strong bg-input/90 outline-none transition-[border-color,box-shadow] duration-fast focus-visible:ring-2 focus-visible:ring-ring/60 disabled:cursor-not-allowed disabled:opacity-60 data-[state=checked]:border-primary",
			className,
		)}
		ref={ref}
		{...props}
	>
		<RadioGroupPrimitive.Indicator className="size-1.5 rounded-full bg-primary" />
	</RadioGroupPrimitive.Item>
));
RadioGroupItem.displayName = "RadioGroupItem";

export { RadioGroup, RadioGroupItem };
