export type SearchResource = "tunnel" | "node" | "user" | "forward" | "all";

export type SearchCacheKey = string;

type CacheEntry<T> = {
  value: T;
  loadedAt: number;
};

/**
 * Role-scoped search data cache with TTL and typed invalidation.
 * Shares one in-flight promise per key; freshness is recorded only on success.
 */
export class SearchCache<T = unknown> {
  private readonly entries = new Map<SearchCacheKey, CacheEntry<T>>();
  private readonly inflight = new Map<SearchCacheKey, Promise<T>>();
  private readonly ttlMs: number;
  private readonly now: () => number;
  private generation = 0;

  constructor(ttlMs = 60_000, now: () => number = () => Date.now()) {
    this.ttlMs = ttlMs;
    this.now = now;
  }

  get(key: SearchCacheKey): T | undefined {
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    if (this.now() - entry.loadedAt > this.ttlMs) {
      this.entries.delete(key);
      return undefined;
    }
    return entry.value;
  }

  async load(key: SearchCacheKey, loader: () => Promise<T>): Promise<T> {
    const cached = this.get(key);
    if (cached !== undefined) return cached;

    const pending = this.inflight.get(key);
    if (pending) return pending;

    const gen = this.generation;
    const keyRef = key;
    const holder: { promise?: Promise<T> } = {};
    holder.promise = loader()
      .then((value) => {
        if (gen === this.generation) {
          this.entries.set(keyRef, { value, loadedAt: this.now() });
        }
        return value;
      })
      .finally(() => {
        if (this.inflight.get(keyRef) === holder.promise) {
          this.inflight.delete(keyRef);
        }
      });
    this.inflight.set(key, holder.promise);
    return holder.promise;
  }

  /**
   * Clear cache entries that may include the given resource.
   * Admin and user search payloads can both include forwards, so unknown resource
   * invalidation drops all role keys.
   */
  invalidate(resource: SearchResource = "all"): void {
    void resource;
    this.generation += 1;
    this.entries.clear();
    this.inflight.clear();
  }

  clear(): void {
    this.invalidate("all");
  }
}

const globalSearchCache = new SearchCache<unknown[]>(60_000);

export function getGlobalSearchCache(): SearchCache<unknown[]> {
  return globalSearchCache;
}

/** Emit invalidation after successful create/update/delete of searchable entities. */
export function invalidateGlobalSearch(resource: SearchResource = "all"): void {
  globalSearchCache.invalidate(resource);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("global-search-invalidate", { detail: { resource } }));
  }
}

export function searchCacheKeyForRole(isAdmin: boolean): SearchCacheKey {
  return isAdmin ? "admin" : "user";
}
