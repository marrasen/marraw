import { Slider as SliderPrimitive } from "@base-ui/react/slider"

import { cn } from "@/lib/utils"

function Slider({
  className,
  defaultValue,
  value,
  min = 0,
  max = 100,
  fillFrom,
  gradient,
  onPointerUp,
  onKeyDown,
  ...props
}: SliderPrimitive.Root.Props & {
  /**
   * Value the filled range grows from. Defaults to `min` (the classic
   * left-anchored fill); pass the control's neutral value to make the fill
   * run from that point to the thumb — e.g. from the center for ±sliders
   * whose default is 0.
   */
  fillFrom?: number
  /**
   * Gradient track classes (e.g. the WB temperature blue→amber ramp). The
   * whole track shows the gradient and no fill range is drawn — the thumb
   * position IS the value.
   */
  gradient?: string
}) {
  // One thumb per actual value. A plain-number value must yield ONE thumb —
  // the previous [min, max] fallback rendered a phantom second thumb that
  // broke base-ui's click-on-track value jumping.
  const _values = Array.isArray(value)
    ? value
    : value != null
      ? [value]
      : Array.isArray(defaultValue)
        ? defaultValue
        : defaultValue != null
          ? [defaultValue]
          : [min]

  // Custom fill origin: base-ui's Indicator always anchors at the track
  // start, so an origin-anchored fill is drawn by hand from percentages.
  const pctOf = (v: number) =>
    max === min ? 0 : (Math.min(max, Math.max(min, v)) - min) / (max - min) * 100
  const useOriginFill = fillFrom != null && _values.length === 1
  const valPct = pctOf(_values[0])
  const originPct = pctOf(fillFrom ?? min)

  // A mouse drag leaves the thumb's hidden <input type="range"> focused, which
  // would swallow the global keymap (photo nav, Esc) — sliders are driven by
  // drag and the +/- hotkeys, never by holding focus. So release focus when a
  // pointer interaction ends (deferred a frame in case base-ui re-focuses the
  // input on pointer-up), and let Esc bail out of a slider that still has it.
  // Keyboard/Tab focus is untouched — no pointer, no blur.
  const blurThumb = () => {
    const el = document.activeElement as HTMLElement | null
    if (el?.tagName === "INPUT" && el.getAttribute("type") === "range") el.blur()
  }

  return (
    <SliderPrimitive.Root
      className={cn("data-horizontal:w-full data-vertical:h-full", className)}
      data-slot="slider"
      defaultValue={defaultValue}
      value={value}
      min={min}
      max={max}
      thumbAlignment="edge"
      onPointerUp={(e) => {
        onPointerUp?.(e)
        requestAnimationFrame(blurThumb)
      }}
      onKeyDown={(e) => {
        onKeyDown?.(e)
        if (e.key === "Escape") blurThumb()
      }}
      {...props}
    >
      <SliderPrimitive.Control className="relative flex w-full touch-none items-center select-none data-disabled:opacity-50 data-vertical:h-full data-vertical:min-h-40 data-vertical:w-auto data-vertical:flex-col">
        <SliderPrimitive.Track
          data-slot="slider-track"
          className={cn(
            "relative grow overflow-hidden rounded-full select-none data-horizontal:h-[3px] data-horizontal:w-full data-vertical:h-full data-vertical:w-[3px]",
            gradient ?? "bg-input",
          )}
        >
          {gradient ? null : useOriginFill ? (
            <span
              data-slot="slider-range"
              className="absolute h-full rounded-full bg-primary select-none"
              style={{
                left: `${Math.min(valPct, originPct)}%`,
                width: `${Math.abs(valPct - originPct)}%`,
              }}
            />
          ) : (
            <SliderPrimitive.Indicator
              data-slot="slider-range"
              className="bg-primary select-none data-horizontal:h-full data-vertical:w-full"
            />
          )}
        </SliderPrimitive.Track>
        {Array.from({ length: _values.length }, (_, index) => (
          <SliderPrimitive.Thumb
            data-slot="slider-thumb"
            key={index}
            className="relative block size-3 shrink-0 rounded-full border border-black/30 bg-white shadow-[0_1px_4px_rgba(0,0,0,.4)] ring-ring/50 transition-[color,box-shadow] select-none after:absolute after:-inset-2 hover:ring-3 focus-visible:ring-3 focus-visible:outline-hidden active:ring-3 disabled:pointer-events-none disabled:opacity-50"
          />
        ))}
      </SliderPrimitive.Control>
    </SliderPrimitive.Root>
  )
}

export { Slider }
