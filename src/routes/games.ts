import express, { Request, Response } from "express";
import { z } from "zod";
import { requireApiKey } from "../middleware/requireApiKey.js";
import { parseBody } from "../schemas/parse.js";
import { createGame, joinGame, getActiveSessionCount } from "../sessions.js";
import { ApiError } from "../errors/ApiError.js";

const router = express.Router();

router.post("/games", requireApiKey, (_req: Request, res: Response) => {
  const project = _req.project!;

  const active = getActiveSessionCount(project.id);
  if (active >= project.maxSessions) {
    throw new ApiError(429, "Max sessions reached for this project");
  }

  const result = createGame(project.id, project.maxPlayersPerSession);
  if (!result) throw new ApiError(500, "Failed to create game");

  return res.status(201).json({
    code: result.code,
    password: result.password,
    sessionToken: result.sessionToken,
  });
});

const JoinSchema = z.object({
  password: z.string().min(1),
});

router.post("/games/:code/join", requireApiKey, (req: Request, res: Response) => {
  const project = req.project!;
  const { password } = parseBody(JoinSchema, req.body);

  const sessionToken = joinGame(req.params.code, password, project.id);
  if (sessionToken === null) throw new ApiError(404, "Invalid code or password");

  return res.json({ sessionToken });
});

export default router;
