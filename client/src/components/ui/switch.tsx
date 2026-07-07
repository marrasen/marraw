import { Switch as SwitchPrimitive } from "@base-ui/react/switch"

import { cn } from "@/lib/utils"

function Switch({ className, ...props }: SwitchPrimitive.Root.Props) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        // Base UI renders the root as an inline <span>; without a block-ish
        // display the w/h utilities are ignored and the track collapses to 0×0.
        "relative inline-block h-5 w-[34px] shrink-0 rounded-full bg-input transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/50 data-checked:bg-primary data-disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="absolute top-0.5 left-0.5 size-4 rounded-full bg-white shadow transition-transform data-checked:translate-x-3.5"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
