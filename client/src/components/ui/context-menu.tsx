import { ContextMenu as ContextMenuPrimitive } from "@base-ui/react/context-menu"

import { cn } from "@/lib/utils"

function ContextMenu({ ...props }: ContextMenuPrimitive.Root.Props) {
  return <ContextMenuPrimitive.Root data-slot="context-menu" {...props} />
}

function ContextMenuTrigger({ ...props }: ContextMenuPrimitive.Trigger.Props) {
  return <ContextMenuPrimitive.Trigger data-slot="context-menu-trigger" {...props} />
}

function ContextMenuContent({
  className,
  ...props
}: ContextMenuPrimitive.Popup.Props) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Positioner className="isolate z-50 outline-none">
        <ContextMenuPrimitive.Popup
          data-slot="context-menu-content"
          className={cn(
            "z-50 flex w-[262px] flex-col gap-px rounded-[11px] border border-glass-border bg-popover/98 p-[7px] text-[13px] text-secondary-foreground shadow-[0_30px_70px_-20px_rgba(0,0,0,.85)] outline-none data-closed:animate-out data-closed:fade-out-0 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95",
            className,
          )}
          {...props}
        />
      </ContextMenuPrimitive.Positioner>
    </ContextMenuPrimitive.Portal>
  )
}

function ContextMenuItem({
  className,
  hint,
  variant = "default",
  ...props
}: ContextMenuPrimitive.Item.Props & {
  /** Right-aligned mono hint, e.g. a shortcut ("Enter", "F2"). */
  hint?: string
  variant?: "default" | "destructive"
}) {
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      data-variant={variant}
      className={cn(
        "flex h-8 shrink-0 cursor-default items-center gap-2.5 rounded-[7px] px-2.5 outline-none select-none data-disabled:pointer-events-none data-disabled:opacity-50 data-highlighted:bg-sidebar-accent data-highlighted:text-foreground [&_svg]:size-3.5 [&_svg]:shrink-0 [&_svg]:text-muted-foreground",
        variant === "destructive" &&
          "h-auto py-1.5 text-danger-text data-highlighted:bg-destructive/10 data-highlighted:text-danger-text [&_svg]:text-danger-text",
        className,
      )}
      {...props}
    >
      {props.children}
      {hint && (
        <span className="ml-auto font-mono text-[10.5px] text-faint">{hint}</span>
      )}
    </ContextMenuPrimitive.Item>
  )
}

function ContextMenuSeparator({
  className,
  ...props
}: ContextMenuPrimitive.Separator.Props) {
  return (
    <ContextMenuPrimitive.Separator
      data-slot="context-menu-separator"
      className={cn("mx-1.5 my-1 h-px bg-border", className)}
      {...props}
    />
  )
}

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
}
