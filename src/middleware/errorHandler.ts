import { NextFunction, Request, Response } from "express";
import { ApiError } from "../errors/ApiError.js";
import logger from "../lib/logger.js";

export function errorHandler(err: unknown, _req: Request, res: Response, _next: NextFunction) {
  if (err instanceof ApiError) {
    return res.status(err.status).json({ error: err.message });
  }

  logger.error({ err }, "Unhandled error");
  return res.status(500).json({ error: "Internal server error" });
}
