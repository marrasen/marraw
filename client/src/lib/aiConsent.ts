// isModelNotDownloaded matches the server's download-consent sentinel
// (aimasks.go's aiModelNotDownloadedMsg): the feature needs model weights the
// user hasn't approved downloading yet — open the AIModelDialog instead of
// surfacing an error. Shared by every AI feature entry point (masks panel,
// auto crop).
export const isModelNotDownloaded = (err: unknown) =>
  err instanceof Error && err.message.includes('model not downloaded');
