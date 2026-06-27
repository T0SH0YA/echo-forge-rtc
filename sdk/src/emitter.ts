// Event emitter mínimo, tipado, sem dependências.
export class Emitter<Events extends Record<string, unknown>> {
  private listeners = new Map<keyof Events, Set<(payload: unknown) => void>>();

  on<K extends keyof Events>(event: K, fn: (payload: Events[K]) => void): () => void {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    set.add(fn as (payload: unknown) => void);
    return () => set!.delete(fn as (payload: unknown) => void);
  }

  off<K extends keyof Events>(event: K, fn: (payload: Events[K]) => void): void {
    this.listeners.get(event)?.delete(fn as (payload: unknown) => void);
  }

  protected emit<K extends keyof Events>(event: K, payload: Events[K]): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const fn of set) {
      try {
        fn(payload);
      } catch (err) {
        console.error(`[sdk] listener for "${String(event)}" threw`, err);
      }
    }
  }
}
