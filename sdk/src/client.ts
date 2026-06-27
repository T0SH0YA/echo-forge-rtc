import { Room } from "./room";
import type { ClientOptions, JoinOptions } from "./types";

export class Client {
  constructor(private readonly opts: ClientOptions) {}

  async join(opts: JoinOptions): Promise<Room> {
    const room = new Room(opts.roomId, this.opts.url, opts.token);
    await room.connect();
    return room;
  }
}
