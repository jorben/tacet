// Deterministic pseudo-random numbers so demo batches and scenes look the same on every run.

export function createRng(seed: number): () => number {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

let current = createRng(417);

/** Restart the shared sequence; batch creation and sampling do this so their output is reproducible. */
export function reseed(seed: number): void {
  current = createRng(seed);
}

/** Integer in [a, b]. */
export const randInt = (a: number, b: number): number => a + Math.floor(current() * (b - a + 1));
export const pick = <T,>(xs: readonly T[]): T => xs[Math.floor(current() * xs.length)]!;
