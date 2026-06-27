// DataChannel wrapper — fina camada sobre RTCDataChannel com reliability
// options expostas e eventos tipados.

import { Emitter } from "./emitter";

export interface DataChannelOptions {
  /** ordered delivery (default true) */
  ordered?: boolean;
  /** unreliable: máximo de retransmissões (mutuamente exclusivo com maxPacketLifeTime) */
  maxRetransmits?: number;
  /** unreliable: tempo de vida do pacote em ms */
  maxPacketLifeTime?: number;
  /** subprotocolo (igual ao do RFC 8832) */
  protocol?: string;
  /** se true, ambos os lados criam o channel com o mesmo id (sem DCEP) */
  negotiated?: boolean;
  id?: number;
}

interface DataChannelEvents extends Record<string, unknown> {
  open: void;
  close: void;
  message: { data: string | ArrayBuffer | Blob; from: string };
  error: Error;
}

export class DataChannel extends Emitter<DataChannelEvents> {
  constructor(
    readonly label: string,
    readonly remotePeerId: string,
    private readonly dc: RTCDataChannel,
  ) {
    super();
    dc.binaryType = "arraybuffer";
    dc.onopen = () => this.emit("open", undefined);
    dc.onclose = () => this.emit("close", undefined);
    dc.onerror = (ev) => {
      const err = (ev as RTCErrorEvent).error ?? new Error("datachannel error");
      this.emit("error", err instanceof Error ? err : new Error(String(err)));
    };
    dc.onmessage = (ev) => this.emit("message", { data: ev.data, from: remotePeerId });
  }

  get readyState(): RTCDataChannelState {
    return this.dc.readyState;
  }

  get bufferedAmount(): number {
    return this.dc.bufferedAmount;
  }

  send(payload: string | ArrayBuffer | ArrayBufferView | Blob): void {
    if (this.dc.readyState !== "open") {
      throw new Error(`datachannel not open (state=${this.dc.readyState})`);
    }
    // overload do RTCDataChannel.send aceita todos esses tipos
    (this.dc as unknown as { send: (p: unknown) => void }).send(payload);
  }

  close(): void {
    this.dc.close();
  }
}
