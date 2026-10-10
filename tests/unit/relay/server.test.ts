import { describe, it, expect, vi, beforeEach } from "vitest";
import type { RemoteInfo } from "dgram";

vi.mock("dgram", () => ({
  default: { createSocket: vi.fn() },
}));

vi.mock("../../../src/services/sessions.js", () => ({
  registerAndGetPeers: vi.fn(),
}));

vi.mock("../../../src/lib/logger.js", () => ({
  default: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() },
}));

vi.mock("../../../src/config/env.js", () => ({
  config: { portUdp: 7777 },
  projects: new Map(),
}));

import dgram from "dgram";
import logger from "../../../src/lib/logger.js";
import * as sessions from "../../../src/services/sessions.js";
import { startRelay } from "../../../src/relay/server.js";

function makeMockSocket() {
  const handlers: Record<string, (...args: unknown[]) => void> = {};
  return {
    on: vi.fn((event: string, handler: (...args: unknown[]) => void) => {
      handlers[event] = handler;
    }),
    bind: vi.fn((_port: number, cb?: () => void) => cb?.()),
    send: vi.fn(),
    close: vi.fn(),
    emit(event: string, ...args: unknown[]) {
      handlers[event]?.(...args);
    },
  };
}

function makePacket(
  sessionToken: number,
  peerSecretHex: string,
  payload = Buffer.alloc(0)
): Buffer {
  const buf = Buffer.allocUnsafe(4 + 16 + payload.length);
  buf.writeUInt32BE(sessionToken, 0);
  Buffer.from(peerSecretHex, "hex").copy(buf, 4);
  payload.copy(buf, 20);
  return buf;
}

const RINFO: RemoteInfo = { address: "1.2.3.4", port: 5000, family: "IPv4", size: 20 };
const SECRET_HEX = "aabbccddeeff00112233445566778899";

let socket: ReturnType<typeof makeMockSocket>;

beforeEach(() => {
  vi.clearAllMocks();
  socket = makeMockSocket();
  vi.mocked(dgram.createSocket).mockReturnValue(socket as never);
  startRelay();
});

describe("startRelay", () => {
  it("binds on the configured UDP port", () => {
    expect(socket.bind).toHaveBeenCalledWith(7777, expect.any(Function));
  });

  it("drops packets shorter than 20 bytes", () => {
    socket.emit("message", Buffer.alloc(19), RINFO);
    expect(sessions.registerAndGetPeers).not.toHaveBeenCalled();
    expect(logger.warn).toHaveBeenCalled();
  });

  it("drops packets with an unknown session or invalid secret", () => {
    vi.mocked(sessions.registerAndGetPeers).mockReturnValue(null);
    socket.emit("message", makePacket(12345, SECRET_HEX), RINFO);
    expect(socket.send).not.toHaveBeenCalled();
    expect(logger.warn).toHaveBeenCalled();
  });

  it("calls registerAndGetPeers with the parsed token, secret, address and port", () => {
    vi.mocked(sessions.registerAndGetPeers).mockReturnValue({ senderIndex: 0, peers: [] });
    socket.emit("message", makePacket(0xdeadbeef, SECRET_HEX), RINFO);
    expect(sessions.registerAndGetPeers).toHaveBeenCalledWith(
      0xdeadbeef,
      SECRET_HEX,
      "1.2.3.4",
      5000
    );
  });

  it("does not forward when there are no other peers", () => {
    vi.mocked(sessions.registerAndGetPeers).mockReturnValue({ senderIndex: 0, peers: [] });
    socket.emit("message", makePacket(1, SECRET_HEX, Buffer.from("hello")), RINFO);
    expect(socket.send).not.toHaveBeenCalled();
  });

  it("forwards to each peer in the result", () => {
    vi.mocked(sessions.registerAndGetPeers).mockReturnValue({
      senderIndex: 0,
      peers: [
        { address: "2.3.4.5", port: 6000 },
        { address: "3.4.5.6", port: 7000 },
      ],
    });
    socket.emit("message", makePacket(1, SECRET_HEX, Buffer.from("data")), RINFO);
    expect(socket.send).toHaveBeenCalledTimes(2);
    expect(socket.send).toHaveBeenCalledWith(
      expect.any(Buffer),
      6000,
      "2.3.4.5",
      expect.any(Function)
    );
    expect(socket.send).toHaveBeenCalledWith(
      expect.any(Buffer),
      7000,
      "3.4.5.6",
      expect.any(Function)
    );
  });

  it("prepends the sender index as 4-byte uint32 BE to the forwarded payload", () => {
    vi.mocked(sessions.registerAndGetPeers).mockReturnValue({
      senderIndex: 2,
      peers: [{ address: "2.3.4.5", port: 6000 }],
    });
    const payload = Buffer.from("gamedata");
    socket.emit("message", makePacket(1, SECRET_HEX, payload), RINFO);

    const forwarded = vi.mocked(socket.send).mock.calls[0][0] as Buffer;
    expect(forwarded.readUInt32BE(0)).toBe(2);
    expect(forwarded.subarray(4)).toEqual(payload);
  });

  it("logs a warning and closes the socket on error", () => {
    socket.emit("error", new Error("bind failed"));
    expect(logger.error).toHaveBeenCalled();
    expect(socket.close).toHaveBeenCalled();
  });
});
