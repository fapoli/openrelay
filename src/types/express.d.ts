import "express";
import { Project } from "../types/project.js";

declare global {
  namespace Express {
    interface Request {
      project?: Project;
    }
  }
}

export {};
