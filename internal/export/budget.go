package export

const (
	// estBytesPerPixel covers one job's peak: Go-side buffers (LibRaw RGB
	// copy, RGBA, geometry copy, detail planes, encode buffer ≈ 11–16 B/px)
	// plus LibRaw's transient C-side 4ch/16-bit intermediates (≈ 8–11 B/px).
	// Measured ≈ 21–26 B/px on a 42 MP file; use the high end.
	estBytesPerPixel = 26
	// estJobFloor absorbs fixed overhead (LibRaw handle, encode slack) so
	// tiny images don't get an unrealistically small estimate.
	estJobFloor = 64 << 20 // 64 MiB
	// defaultJobPixels is assumed when a photo's dimensions are unknown
	// (metadata not yet backfilled): a 62 MP full-frame body, conservative.
	defaultJobPixels = 62_000_000
	// budgetFraction of available physical RAM the batch may claim; the rest
	// covers the UI, the pyramid cache, and OS file-cache churn.
	budgetFraction = 0.70
	// minBudget keeps the semaphore sane on a starved machine; single jobs
	// clamp to it and run alone.
	minBudget = 1 << 30 // 1 GiB
	// fallbackAvail stands in for available RAM when the probe fails.
	fallbackAvail = 8 << 30 // 8 GiB
)

// jobWeight estimates peak bytes for one render job from its pixel count.
// Unknown dimensions (width/height ≤ 0) assume defaultJobPixels.
func jobWeight(width, height int) int64 {
	px := int64(width) * int64(height)
	if width <= 0 || height <= 0 {
		px = defaultJobPixels
	}
	return max(px*estBytesPerPixel, estJobFloor)
}

// exportBudget converts available physical bytes into the admission budget.
func exportBudget(availPhys uint64) int64 {
	return max(int64(float64(availPhys)*budgetFraction), minBudget)
}
