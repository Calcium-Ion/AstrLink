export class RequestGate {
  private generation = 0;
  private exclusive = false;

  begin(): number | null {
    if (this.exclusive) return null;
    this.generation += 1;
    return this.generation;
  }

  beginExclusive(): number | null {
    if (this.exclusive) return null;
    this.exclusive = true;
    this.generation += 1;
    return this.generation;
  }

  isCurrent(generation: number): boolean {
    return generation === this.generation;
  }

  endExclusive(generation: number): boolean {
    if (!this.exclusive || generation !== this.generation) return false;
    this.exclusive = false;
    return true;
  }

  invalidate(): void {
    this.generation += 1;
  }
}
