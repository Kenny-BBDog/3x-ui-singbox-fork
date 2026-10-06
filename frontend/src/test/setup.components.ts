import { afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';

import enUS from '../../../internal/web/translation/en-US.json';

// RTL sets this from a global beforeAll, which never runs with `globals: false`.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('persian-calendar-suite', () => ({
  PersianDateTimePicker: () => null,
}));

if (typeof globalThis.localStorage === 'undefined') {
  const store = new Map<string, string>();
  const storage = {
    getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
    setItem: (k: string, v: string) => {
      store.set(k, String(v));
    },
    removeItem: (k: string) => {
      store.delete(k);
    },
    clear: () => {
      store.clear();
    },
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size;
    },
  } as Storage;
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true });
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true });
}

if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

// jsdom does not implement pseudo-element styles or Range geometry. Ant
// Design and CodeMirror use these APIs for layout, so supply harmless test
// fallbacks instead of emitting noisy "Not implemented" errors.
const nativeGetComputedStyle = window.getComputedStyle.bind(window);
window.getComputedStyle = ((element: Element) =>
  nativeGetComputedStyle(element)) as typeof window.getComputedStyle;

if (!Range.prototype.getClientRects) {
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList;
}

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    lng: 'en-US',
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: enUS } },
    interpolation: { escapeValue: false, prefix: '{', suffix: '}' },
    returnNull: false,
  });
}

// Drain the event-loop phases React's work can be queued on.
//
// React 19 dispatches its scheduled work from a check-phase callback
// (`setImmediate`; the CI stack shows `processImmediate node:internal/timers`),
// and that callback reads `window.event`. If it is still pending when vitest
// tears the jsdom environment down it throws "window is not defined" — no
// assertion fails, but vitest counts an unhandled error and the run exits
// non-zero.
//
// Draining only the timers phase (the previous `setTimeout` loop) cannot flush a
// check-phase callback, and an `act` flush only pumps React's own act queue and
// microtasks — neither reaches the phase React actually uses. Alternating both
// phases is what makes the flush complete: Node runs check-phase callbacks
// FIFO, so awaiting `setImmediate` runs the pending one, and awaiting
// `setTimeout` clears anything that queued a timer in turn.
async function drainEventLoopPhases(): Promise<void> {
  const immediate =
    typeof (globalThis as { setImmediate?: unknown }).setImmediate === 'function'
      ? (cb: () => void) => setImmediate(cb)
      : (cb: () => void) => setTimeout(cb, 0);
  for (let i = 0; i < 3; i += 1) {
    await new Promise<void>((resolve) => immediate(resolve));
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
  }
}

afterEach(async () => {
  // Before unmounting: flush work the test itself left queued.
  await drainEventLoopPhases();
  cleanup();
  document.body.innerHTML = '';
  // After unmounting: unmounting is what schedules React's passive-effect flush,
  // so the queue is populated here, not before. This is the half the previous
  // ordering missed.
  await drainEventLoopPhases();
});

import { HttpUtil, Msg } from '@/utils';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, obj: {} } as any);
vi.spyOn(HttpUtil, 'get').mockImplementation(
  async (url: string) => new Msg(true, '', url.includes('/panel/api/inbounds/options') ? [] : {}),
);
