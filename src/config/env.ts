import { Project } from "../types/project.js";
import { readFileSync } from "fs";
import { load } from "js-yaml";
import { z } from "zod";
import logger from "./lib/logger.js";

const ProjectYamlSchema = z.object({
  id: z.string(),
  maxSessions: z.number().int().positive().default(100),
  maxPlayersPerSession: z.number().int().positive().default(16),
});

const ProjectsFileSchema = z.object({
  projects: z.array(ProjectYamlSchema),
});


function loadProjects(): Map<string, Project> {
  const map = new Map<string, Project>();

  let file: z.infer<typeof ProjectsFileSchema> = { projects: [] };
  try {
    const raw = readFileSync("projects.yaml", "utf-8");
    file = ProjectsFileSchema.parse(load(raw));
  } catch {
    logger.warn("No projects.yaml found — no projects loaded");
  }

  for (const p of file.projects) {
    const envKey = `${p.id.toUpperCase().replace(/-/g, "_")}_API_KEY`;
    const apiKey = process.env[envKey];
    if (!apiKey) {
      logger.warn({ projectId: p.id, envKey }, "No API key found for project — skipping");
      continue;
    }
    map.set(apiKey, {
      id: p.id,
      apiKey,
      maxSessions: p.maxSessions,
      maxPlayersPerSession: p.maxPlayersPerSession,
    });
    logger.info({ projectId: p.id }, "Project loaded");
  }

  return map;
}

export const projects = loadProjects();

export const config = {
  portHttp: parseInt(process.env.PORT_HTTP ?? "3000"),
  portUdp: parseInt(process.env.PORT_UDP ?? "7777"),
  sessionTtlMs: parseInt(process.env.SESSION_TTL_MS ?? "300000"),
};
