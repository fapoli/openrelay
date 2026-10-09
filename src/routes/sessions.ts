import express, { Request, Response } from "express";
import { z } from "zod";
import { requireApiKey } from "../middleware/requireApiKey.js";
import { parseBody } from "../schemas/parse.js";
import { createSession, joinSession, getActiveSessionCount } from "../sessions.js";
import { ApiError } from "../errors/ApiError.js";

const router = express.Router();

router.post("/sessions", requireApiKey, (_req: Request, res: Response) => {
  const project = _req.project!;

  const active = getActiveSessionCount(project.id);
  if (active >= project.maxSessions) {
    throw new ApiError(429, "Max sessions reached for this project");
  }

  const result = createSession(project.id, project.maxPlayersPerSession);
  if (!result) throw new ApiError(500, "Failed to create session");

  return res.status(201).json({
    code: result.code,
    password: result.password,
    sessionToken: result.sessionToken,
    peerSecret: result.peerSecret,
  });
});

const JoinSchema = z.object({
  password: z.string().min(1),
});

router.post("/sessions/:code/join", requireApiKey, (req: Request, res: Response) => {
  const project = req.project!;
  const { password } = parseBody(JoinSchema, req.body);

  const result = joinSession(req.params.code, password, project.id);
  if (result === null) throw new ApiError(404, "Invalid code or password");

  return res.json({ sessionToken: result.sessionToken, peerSecret: result.peerSecret });
});

export default router;
