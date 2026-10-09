import dgram from "dgram";
import { config } from "./config.js";
import { registerAndGetPeers } from "./sessions.js";
import logger from "./lib/logger.js";

// Packet format: [4 bytes sessionToken (uint32 BE)] [16 bytes peerSecret (hex bytes)] [payload...]

export function startRelay(): void {
  const server = dgram.createSocket("udp4");

  server.on("message", (msg, rinfo) => {
    logger.debug({ bytes: msg.length, from: `${rinfo.address}:${rinfo.port}` }, "UDP packet received");

    if (msg.length < 20) {
      logger.warn({ bytes: msg.length, from: `${rinfo.address}:${rinfo.port}` }, "UDP packet too short, dropping");
      return;
    }

    const sessionToken = msg.readUInt32BE(0);
    const peerSecret = msg.subarray(4, 20).toString("hex");
    const payload = msg.subarray(20);

    const result = registerAndGetPeers(sessionToken, peerSecret, rinfo.address, rinfo.port);
    if (!result) {
      logger.warn({ sessionToken, from: `${rinfo.address}:${rinfo.port}` }, "UDP packet dropped: unknown session or invalid secret");
      return;
    }

    logger.debug({ sessionToken, senderIndex: result.senderIndex, peers: result.peers.length, payloadBytes: payload.length }, "Forwarding UDP packet");

    // Prepend 4-byte sender index (big-endian) so receivers can identify the source peer
    const forwarded = Buffer.allocUnsafe(4 + payload.length);
    forwarded.writeUInt32BE(result.senderIndex, 0);
    payload.copy(forwarded, 4);

    for (const peer of result.peers) {
      server.send(forwarded, peer.port, peer.address, (err) => {
        if (err) logger.warn({ err, peer }, "Failed to forward packet");
      });
    }
  });

  server.on("error", (err) => {
    logger.error({ err }, "UDP relay error");
    server.close();
  });

  server.bind(config.portUdp, () => {
    logger.info({ port: config.portUdp }, "UDP relay listening");
  });
}
