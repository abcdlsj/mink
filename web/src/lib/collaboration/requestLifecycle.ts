export class PayloadRequestLifecycle<T> {
  private fingerprint: string | undefined;
  private requestId: string;

  constructor(
    private readonly identify: (payload: T) => string = (payload) =>
      JSON.stringify(payload),
    private readonly nextId: () => string = () => crypto.randomUUID(),
  ) {
    this.requestId = nextId();
  }

  sync(payload: T): string {
    const nextFingerprint = this.identify(payload);
    if (
      this.fingerprint !== undefined &&
      this.fingerprint !== nextFingerprint
    ) {
      this.requestId = this.nextId();
    }
    this.fingerprint = nextFingerprint;
    return this.requestId;
  }

  complete(): void {
    this.fingerprint = undefined;
    this.requestId = this.nextId();
  }
}
