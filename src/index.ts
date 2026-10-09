import "dotenv/config";
import "express-async-errors";
import express from "express";
import cors from "cors";
import { pinoHttp } from "pino-http";
import logger from "./lib/logger.js";
import { config } from "./config.js";
import gamesRouter from "./routes/games.js";
import { errorHandler } from "./middleware/errorHandler.js";
import { startRelay } from "./relay.js";
import { cleanup } from "./sessions.js";

const app = express();
app.use(cors());
app.use(express.json());
app.use(pinoHttp({ logger, redact: ["req.headers.authorization"] }));

app.get("/health", (_req, res) => res.json({ status: "ok" }));
app.use("/", gamesRouter);
app.use(errorHandler);

app.listen(config.portHttp, () => {
  logger.info({ port: config.portHttp }, "HTTP signaling listening");
});

startRelay();

setInterval(cleanup, 30_000);

process.on("SIGTERM", () => process.exit(0));
process.on("SIGINT", () => process.exit(0));
