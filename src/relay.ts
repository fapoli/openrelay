import dgram from "dgram";
import { config } from "./config.js";
import { registerOrUpdatePeer } from "./sessions.js";
import logger from "./lib/logger.js";

// Packet format: [4 bytes sessionToken (uint32 BE)] [payload...]

export function startRelay(): void {
  const server = dgram.createSocket("udp4");

  server.on("message", (msg, rinfo) => {
    if (msg.length < 5) return; // need at least token + 1 byte payload

    const sessionToken = msg.readUInt32BE(0);
    const payload = msg.subarray(4);

    const sessionPeers = registerOrUpdatePeer(sessionToken, rinfo.address, rinfo.port);
    if (!sessionPeers) return; // unknown session — drop

    for (const peer of sessionPeers) {
      if (peer.address === rinfo.address && peer.port === rinfo.port) continue;
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
