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

// Track timers scheduled during a test, so teardown can cancel the ones still
// pending instead of waiting for them.
//
// React 19 dispatches its scheduled work from a check-phase callback
// (`setImmediate`; the CI stack shows `processImmediate node:internal/timers`) and
// that callback reads `window.event`. If it is still pending when vitest tears the
// jsdom environment down, it throws "window is not defined": no assertion fails,
// but vitest counts an unhandled error and the run exits non-zero. rc-motion
// (AntD's dropdown and modal animations) keeps rescheduling, so a bounded drain
// either finishes too early or never converges — measured: a converging drain made
// the components project take 1178s instead of 173s and still hit its ceiling.
//
// Cancelling is deterministic and cheap: nothing that a test left pending should
// outlive the test anyway.
const liveTimers = new Set<ReturnType<typeof setTimeout>>();
type TimerFn = (handler: TimerHandler, timeout?: number, ...args: unknown[]) => unknown;

// The untracked scheduler, captured before the wrappers below replace the global.
// Used by the flush so the flush itself is never counted as leaked work.
const nativeSetTimeout = setTimeout;

function trackTimers(): void {
  const g = globalThis as Record<string, unknown>;
  for (const name of ['setTimeout', 'setInterval'] as const) {
    const original = g[name];
    if (typeof original !== 'function') {
      continue;
    }
    const fn = original as TimerFn;
    g[name] = (handler: TimerHandler, timeout?: number, ...args: unknown[]) => {
      const id = fn(handler, timeout, ...args) as ReturnType<typeof setTimeout>;
      liveTimers.add(id);
      return id;
    };
  }
}

function cancelPendingTimers(): void {
  const g = globalThis as Record<string, unknown>;
  const clearTimeoutFn = g.clearTimeout as ((id: unknown) => void) | undefined;
  const clearIntervalFn = g.clearInterval as ((id: unknown) => void) | undefined;
  for (const id of liveTimers) {
    clearTimeoutFn?.(id);
    clearIntervalFn?.(id);
  }
  liveTimers.clear();
}

trackTimers();

// Flush what is already queued, cheaply: let pending macrotasks and microtasks
// run, without waiting for anything that keeps rescheduling. Combined with
// cancelPendingTimers this covers both halves — run what is due now, drop what is
// still pending.
async function flushQueuedWork(): Promise<void> {
  await Promise.resolve();
  await new Promise<void>((resolve) => {
    nativeSetTimeout(resolve, 0);
  });
}

afterEach(async () => {
  // Run work the test left due, then unmount (which is itself what schedules
  // React's passive-effect flush), then run that too.
  await flushQueuedWork();
  cleanup();
  document.body.innerHTML = '';
  await flushQueuedWork();
  // Whatever is still pending would otherwise fire after jsdom is gone. Cancelling
  // it is safe: a test that leaks a timer has no assertion left to satisfy.
  cancelPendingTimers();
});

import { HttpUtil, Msg } from '@/utils';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, obj: {} } as any);
vi.spyOn(HttpUtil, 'get').mockImplementation(
  async (url: string) => new Msg(true, '', url.includes('/panel/api/inbounds/options') ? [] : {}),
);
