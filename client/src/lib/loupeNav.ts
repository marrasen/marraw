// loupeNav is a tiny out-of-React channel that publishes the loupe's live
// pan/zoom state so a navigator can live somewhere other than on top of the
// canvas — the Develop drawer's Info tab reads viewport/scale/isFit here and
// drives panTo, instead of the floating NavigatorInset that the always-visible
// drawer would cover. CinemaImage is the sole writer.
import { create } from 'zustand';

interface LoupeNavState {
  // Visible region as fractions of the image box: [x, y, w, h].
  viewport: [number, number, number, number];
  scale: number;
  isFit: boolean;
  // Center the main viewport on this image fraction; null when no loupe is up.
  panTo: ((fx: number, fy: number) => void) | null;
}

export const useLoupeNav = create<LoupeNavState>(() => ({
  viewport: [0, 0, 1, 1],
  scale: 1,
  isFit: true,
  panTo: null,
}));

export function setLoupeNav(patch: Partial<LoupeNavState>) {
  useLoupeNav.setState(patch);
}
