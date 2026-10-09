import { NextFunction, Request, Response } from "express";
import { projects } from "../config.js";
import { ApiError } from "../errors/ApiError.js";

export function requireApiKey(req: Request, _res: Response, next: NextFunction): void {
  const auth = req.headers.authorization;
  if (!auth?.startsWith("Bearer ")) throw new ApiError(401, "Missing authorization header");

  const apiKey = auth.slice(7);
  const project = projects.get(apiKey);
  if (!project) throw new ApiError(401, "Invalid API key");

  req.project = project;
  next();
}
