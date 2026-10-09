import dgram from "dgram";
import { config } from "./config.js";
import { registerAndGetPeers } from "./sessions.js";
import logger from "./lib/logger.js";

// Packet format: [4 bytes sessionToken (uint32 BE)] [16 bytes peerSecret (hex bytes)] [payload...]

export function startRelay(): void {
  const server = dgram.createSocket("udp4");

  server.on("message", (msg, rinfo) => {
    if (msg.length < 21) return; // 4 + 16 + 1 minimum

    const sessionToken = msg.readUInt32BE(0);
    const peerSecret = msg.subarray(4, 20).toString("hex");
    const payload = msg.subarray(20);

    const peers = registerAndGetPeers(sessionToken, peerSecret, rinfo.address, rinfo.port);
    if (!peers) return; // unknown session or invalid secret — drop silently

    for (const peer of peers) {
      server.send(payload, peer.port, peer.address, (err) => {
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
