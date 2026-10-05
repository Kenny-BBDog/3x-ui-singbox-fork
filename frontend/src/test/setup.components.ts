import { afterEach, vi } from 'vitest';
import { act, cleanup } from '@testing-library/react';
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

// This jsdom environment has no `setImmediate`, so React's scheduler falls back
// to a MessageChannel port callback to dispatch its work. That is a macrotask no
// `setTimeout` can flush, and if it is still queued at teardown it runs with
// `window` gone and throws "window is not defined" — an unhandled error that
// fails the run even though every assertion passed.
//
// Supplying `setImmediate` puts the scheduler back on a channel a drain (and
// `act`) can reach. Defined before React is imported, because the scheduler
// chooses its channel once, at load.
if (typeof (globalThis as { setImmediate?: unknown }).setImmediate === 'undefined') {
  (globalThis as unknown as { setImmediate: (cb: () => void) => unknown }).setImmediate = (
    cb: () => void,
  ) => setTimeout(cb, 0);
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

afterEach(async () => {
  /*
   * Flush React's pending work BEFORE unmounting, then unmount.
   *
   * React 19 dispatches its work from a scheduler task whose callback reads
   * `window.event`. If work is still queued when vitest tears the jsdom
   * environment down, that callback runs with `window` gone and throws
   * "window is not defined". No assertion fails, but vitest counts it as an
   * unhandled error and the whole run exits non-zero — which is how this failed
   * repeatedly on CI while passing locally (CodeMirror and AntD schedule
   * follow-up layout work, so the queue depth depends on the test).
   *
   * Why a plain timeout drain was not enough: React's scheduler picks its
   * dispatch channel at load time. It prefers `setImmediate`, and falls back to
   * a `MessageChannel` port callback when `setImmediate` is absent — which is
   * the case in this jsdom environment. A MessageChannel callback is a
   * macrotask that `setTimeout` never flushes, so draining timers (the previous
   * fixed three-tick loop, and an `act` flush) could still leave work pending.
   * Measured: deleting `setImmediate` in this environment turns a clean run into
   * 155 unhandled errors, which is the same class of failure.
   *
   * `setImmediateShim` above makes the scheduler use a channel a drain can
   * reach, and `act` then waits for React's real queue instead of guessing.
   */
  await act(async () => {});
  cleanup();
  document.body.innerHTML = '';
});

import { HttpUtil, Msg } from '@/utils';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, obj: {} } as any);
vi.spyOn(HttpUtil, 'get').mockImplementation(
  async (url: string) => new Msg(true, '', url.includes('/panel/api/inbounds/options') ? [] : {}),
);
